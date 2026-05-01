// Package arch plans parent tickets and manages wave rollup.
package arch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/data"
	"github.com/randheer094/velocity/internal/db"
	"github.com/randheer094/velocity/internal/git"
	"github.com/randheer094/velocity/internal/jira"
	"github.com/randheer094/velocity/internal/llm"
	"github.com/randheer094/velocity/internal/status"
)

var (
	inFlight   = map[string]struct{}{}
	inFlightMu sync.Mutex
)

func claim(parentKey string) bool {
	inFlightMu.Lock()
	defer inFlightMu.Unlock()
	if _, ok := inFlight[parentKey]; ok {
		return false
	}
	inFlight[parentKey] = struct{}{}
	return true
}

func release(parentKey string) {
	inFlightMu.Lock()
	delete(inFlight, parentKey)
	inFlightMu.Unlock()
}

// Run plans a parent ticket end-to-end. Invoke via webhook.Enqueue.
func Run(ctx context.Context, parentKey, repoURL, title, requirement string) {
	if !claim(parentKey) {
		slog.Info("arch: planning already in flight, dropping duplicate", "key", parentKey)
		return
	}
	defer release(parentKey)

	runCtx, cancel := context.WithCancel(ctx)
	registerCancel(parentKey, cancel)
	defer func() {
		unregisterCancel(parentKey)
		cancel()
	}()

	stage := "init"
	defer func() {
		if r := recover(); r != nil {
			recordFailure(runCtx, parentKey, "panic", fmt.Errorf("%v", r))
		}
	}()

	if err := plan(runCtx, parentKey, repoURL, title, requirement, &stage); err != nil {
		recordFailure(runCtx, parentKey, stage, err)
	}
}

func plan(ctx context.Context, parentKey, repoURL, title, requirement string, stage *string) error {
	*stage = "load-config"
	cfg := config.Get()
	if cfg == nil {
		return errors.New("config not loaded")
	}
	client := jira.Shared()
	if client == nil {
		return errors.New("jira client not initialised")
	}

	*stage = "retry-guard"
	existing, err := db.GetPlan(ctx, parentKey)
	if err != nil {
		return fmt.Errorf("retry guard: %w", err)
	}
	if existing != nil {
		switch existing.Status {
		case data.PlanDone:
			slog.Info("arch: plan terminal, ignoring re-assignment", "key", parentKey, "status", existing.Status)
			return nil
		case data.PlanCoding:
			if existing.ActiveWaveIdx < len(existing.Waves) {
				slog.Info("arch: plan active, enqueueing advance", "key", parentKey)
				EnqueueFn(kindAdvanceWave, "arch.AdvanceWave:"+parentKey,
					map[string]any{"parent_key": parentKey})
				return nil
			}
		case data.PlanPlanningFailed:
			slog.Info("arch: wiping prior failed plan for retry", "key", parentKey)
			if err := db.WipePlanChildren(ctx, parentKey); err != nil {
				return fmt.Errorf("wipe plan children: %w", err)
			}
		}
	}

	*stage = "transition-planning"
	if planning := status.TaskJiraName(status.Planning); planning != "" {
		if !client.Transition(ctx, parentKey, planning) {
			slog.Error("arch: transition to planning failed", "key", parentKey, "target", planning)
		}
	}

	*stage = "verify-token"
	if strings.Contains(repoURL, "github.com") && os.Getenv(config.GithubTokenEnv) == "" {
		return fmt.Errorf("%s env var not set", config.GithubTokenEnv)
	}

	*stage = "clone"
	workspace := config.WorkspacePath(parentKey)
	_ = os.RemoveAll(workspace)
	if err := os.MkdirAll(config.WorkspaceDir, 0o755); err != nil {
		return fmt.Errorf("mkdir workspaces: %w", err)
	}
	if err := git.Clone(ctx, repoURL, workspace); err != nil {
		return fmt.Errorf("clone: %w", err)
	}

	*stage = "render-prompt"
	prompt, err := buildArchPrompt(parentKey, requirement)
	if err != nil {
		return fmt.Errorf("render arch prompt: %w", err)
	}

	*stage = "arch-llm"
	opts := llm.OptionsFromRoleConfig(cfg.LLM.Arch, workspace)
	output, err := llm.RunPrompt(ctx, prompt, opts)
	if err != nil {
		return fmt.Errorf("arch llm: %w", err)
	}

	*stage = "parse-plan"
	parsed, err := extractPlan(output)
	if err != nil {
		return fmt.Errorf("parse plan: %w; raw=%s", err, trunc(output, 400))
	}
	if len(parsed.Waves) == 0 {
		return errors.New("arch returned empty waves")
	}
	totalTasks := 0
	for _, w := range parsed.Waves {
		totalTasks += len(w.Tasks)
	}
	if totalTasks == 0 {
		return errors.New("arch returned waves with no tasks")
	}

	p := &data.Plan{
		ParentJiraKey: parentKey,
		Name:          title,
		RepoURL:       repoURL,
		Waves:         parsed.Waves,
		ActiveWaveIdx: 0,
		Status:        data.PlanCoding,
		JiraStatus:    status.TaskJiraName(status.Coding),
		CreatedAt:     time.Now().UTC(),
	}

	*stage = "create-subtasks"
	projectKey := jira.ProjectKeyOf(parentKey)
	if projectKey == "" {
		return fmt.Errorf("cannot derive project key from %q", parentKey)
	}
	for waveIdx := range p.Waves {
		for taskIdx := range p.Waves[waveIdx].Tasks {
			t := &p.Waves[waveIdx].Tasks[taskIdx]
			if t.JiraKey != "" {
				continue
			}
			description := t.Description
			if description == "" {
				description = t.Title
			}
			key := client.CreateSubtask(ctx, projectKey, t.Title, description, parentKey)
			if key == "" {
				return fmt.Errorf("failed to create sub-task %q (wave %d, idx %d)", t.Title, waveIdx, taskIdx)
			}
			t.JiraKey = key
			slog.Info("arch: created subtask", "parent", parentKey, "title", t.Title, "jira", key)
		}
	}

	if content := buildWavesComment(p.Waves); len(content) > 0 {
		if !client.CommentIssueADF(ctx, parentKey, content) {
			slog.Warn("arch: failed to post plan comment", "key", parentKey)
		}
	}

	*stage = "save-plan"
	if err := db.SavePlan(ctx, p); err != nil {
		return fmt.Errorf("save plan: %w", err)
	}

	*stage = "transition-coding"
	if coding := status.TaskJiraName(status.Coding); coding != "" {
		if !client.Transition(ctx, parentKey, coding) {
			slog.Error("arch: transition to coding failed", "key", parentKey, "target", coding)
		}
	}

	*stage = "enqueue-assign-wave-0"
	EnqueueFn(kindAssignWave, "arch.AssignWave:"+parentKey+":0",
		map[string]any{"parent_key": parentKey, "wave_idx": 0})
	return nil
}

// AdvanceWave evaluates whether the parent's active wave is complete
// and enqueues the next step (re-advance, AssignWave, or Archive).
// It does not inline-call any other kind: each branch returns after
// at most one DB write plus one enqueue.
func AdvanceWave(ctx context.Context, parentKey string) error {
	p, err := db.GetPlan(ctx, parentKey)
	if err != nil {
		return err
	}
	if p == nil {
		slog.Info("arch: no plan for parent, nothing to advance", "key", parentKey)
		return nil
	}
	client := jira.Shared()
	if client == nil {
		return errors.New("jira client not initialised")
	}
	if p.ActiveWaveIdx >= len(p.Waves) {
		EnqueueFn(kindArchive, "arch.Archive:"+parentKey,
			map[string]any{"parent_key": parentKey})
		return nil
	}

	active := p.Waves[p.ActiveWaveIdx]
	keys := keysOf(active)
	if len(keys) == 0 {
		p.ActiveWaveIdx++
		if err := db.SavePlan(ctx, p); err != nil {
			return err
		}
		EnqueueFn(kindAdvanceWave, "arch.AdvanceWave:"+parentKey,
			map[string]any{"parent_key": parentKey})
		return nil
	}
	issues := client.GetTasksStatus(ctx, keys)
	if !allDone(issues, keys) {
		slog.Info("arch: wave still in progress", "key", parentKey, "wave", p.ActiveWaveIdx)
		return nil
	}

	p.ActiveWaveIdx++
	if err := db.SavePlan(ctx, p); err != nil {
		return err
	}
	if p.ActiveWaveIdx >= len(p.Waves) {
		EnqueueFn(kindArchive, "arch.Archive:"+parentKey,
			map[string]any{"parent_key": parentKey})
		return nil
	}
	EnqueueFn(kindAssignWave,
		"arch.AssignWave:"+parentKey+":"+strconv.Itoa(p.ActiveWaveIdx),
		map[string]any{"parent_key": parentKey, "wave_idx": p.ActiveWaveIdx})
	return nil
}

// AssignWave assigns every sub-task in one wave to the developer.
// Loop stays inline — the user's "one task per event" rule covers
// logical steps, not per-iteration enqueues.
func AssignWave(ctx context.Context, parentKey string, waveIdx int) error {
	p, err := db.GetPlan(ctx, parentKey)
	if err != nil {
		return err
	}
	if p == nil {
		slog.Info("arch: no plan for parent, skipping assign", "key", parentKey)
		return nil
	}
	cfg := config.Get()
	if cfg == nil {
		return errors.New("config not loaded")
	}
	client := jira.Shared()
	if client == nil {
		return errors.New("jira client not initialised")
	}
	if waveIdx < 0 || waveIdx >= len(p.Waves) {
		return fmt.Errorf("wave idx %d out of range", waveIdx)
	}
	for _, t := range p.Waves[waveIdx].Tasks {
		key := t.JiraKey
		if key == "" {
			slog.Warn("arch: wave task missing jira key", "title", t.Title, "parent", parentKey)
			continue
		}
		if !client.Assign(ctx, key, cfg.Jira.DeveloperJiraID) {
			slog.Error("arch: assign failed", "key", key)
		}
	}
	slog.Info("arch: wave assigned", "parent", parentKey, "wave", waveIdx, "count", len(p.Waves[waveIdx].Tasks))
	return nil
}

// Archive transitions the parent to DONE, marks the plan done in the
// DB, and removes the parent + every sub-task workspace. When an LLM
// run is still in flight for the parent, the workspace cleanup is
// skipped (the in-flight Run owns those files); the next Run's
// pre-clone RemoveAll handles eventual cleanup.
func Archive(ctx context.Context, parentKey string) error {
	p, err := db.GetPlan(ctx, parentKey)
	if err != nil {
		return err
	}
	if p == nil {
		return nil
	}
	client := jira.Shared()
	if client == nil {
		return errors.New("jira client not initialised")
	}
	done := status.TaskJiraName(status.Done)
	if done != "" {
		if !client.Transition(ctx, parentKey, done) {
			return fmt.Errorf("failed to transition parent %s to %s", parentKey, done)
		}
	}
	if err := db.MarkPlanDone(ctx, parentKey, done); err != nil {
		slog.Warn("arch: failed to mark plan done", "key", parentKey, "err", err)
	}
	if IsInFlight(parentKey) {
		slog.Warn("arch: parent run in flight, skipping workspace cleanup", "key", parentKey)
	} else {
		_ = os.RemoveAll(config.WorkspacePath(parentKey))
		for _, w := range p.Waves {
			for _, t := range w.Tasks {
				if t.JiraKey != "" {
					_ = os.RemoveAll(config.WorkspacePath(t.JiraKey))
				}
			}
		}
	}
	slog.Info("arch: parent done, plan archived (DB retained)", "key", parentKey)
	return nil
}

func keysOf(w data.Wave) []string {
	out := make([]string, 0, len(w.Tasks))
	for _, t := range w.Tasks {
		if t.JiraKey != "" {
			out = append(out, t.JiraKey)
		}
	}
	return out
}

// Dismissed counts as terminal for wave math — a dismissed sub-task
// unblocks successors the same way a merged one does. "Dismissed" is
// an alias of the Done bucket, so a single canonical check suffices.
func allDone(issues map[string]status.IssueInfo, keys []string) bool {
	for _, k := range keys {
		info, ok := issues[k]
		if !ok {
			return false
		}
		if status.SubtaskCanonical(info.Status) != status.Done {
			return false
		}
	}
	return true
}

type rawPlan struct {
	Waves []data.Wave `json:"waves"`
}

// extractPlan accepts the tagged block (preferred) or the last
// balanced JSON object in arch's free-text output.
func extractPlan(raw string) (*rawPlan, error) {
	if i := strings.Index(raw, planBegin); i >= 0 {
		rest := raw[i+len(planBegin):]
		if j := strings.Index(rest, planEnd); j >= 0 {
			return parseRawPlan(strings.TrimSpace(rest[:j]))
		}
	}
	if blob := lastJSONObject(raw); blob != "" {
		return parseRawPlan(blob)
	}
	return nil, errors.New("no plan JSON found in arch output")
}

func parseRawPlan(s string) (*rawPlan, error) {
	var p rawPlan
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func lastJSONObject(s string) string {
	end := strings.LastIndexByte(s, '}')
	if end < 0 {
		return ""
	}
	depth := 0
	for i := end; i >= 0; i-- {
		switch s[i] {
		case '}':
			depth++
		case '{':
			depth--
			if depth == 0 {
				return s[i : end+1]
			}
		}
	}
	return ""
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
