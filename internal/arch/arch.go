// Package arch plans parent tickets and manages wave rollup.
package arch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
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
	existing, _ := db.GetPlan(ctx, parentKey)
	if existing != nil {
		switch existing.Status {
		case data.PlanDone, data.PlanDismissed:
			slog.Info("arch: plan terminal, ignoring re-assignment", "key", parentKey, "status", existing.Status)
			return nil
		case data.PlanActive:
			if existing.ActiveWaveIdx < len(existing.Waves) {
				slog.Info("arch: plan active, advancing instead of re-planning", "key", parentKey)
				return AdvanceWave(ctx, parentKey)
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
		client.Transition(parentKey, planning)
	}

	*stage = "clone"
	workspace := config.WorkspacePath(parentKey)
	_ = os.RemoveAll(workspace)
	if err := os.MkdirAll(config.WorkspaceDir, 0o755); err != nil {
		return fmt.Errorf("mkdir workspaces: %w", err)
	}
	if err := git.Clone(repoURL, workspace); err != nil {
		return fmt.Errorf("clone: %w", err)
	}

	*stage = "arch-llm"
	prompt := buildArchPrompt(parentKey, requirement)
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
	if len(parsed.TaskList) == 0 {
		return errors.New("arch returned empty task_list")
	}
	if len(parsed.Waves) == 0 {
		return errors.New("arch returned empty waves")
	}

	p := &data.Plan{
		ParentJiraKey: parentKey,
		Name:          title,
		RepoURL:       repoURL,
		TaskList:      parsed.TaskList,
		Waves:         parsed.Waves,
		ActiveWaveIdx: 0,
		Status:        data.PlanActive,
		CreatedAt:     time.Now().UTC(),
	}

	*stage = "create-subtasks"
	projectKey := jira.ProjectKeyOf(parentKey)
	if projectKey == "" {
		return fmt.Errorf("cannot derive project key from %q", parentKey)
	}
	keyByID := map[string]string{}
	for i, t := range p.TaskList {
		if t.JiraKey != "" {
			keyByID[t.ID] = t.JiraKey
			continue
		}
		description := t.Description
		if description == "" {
			description = t.Title
		}
		key := client.CreateSubtask(projectKey, t.Title, description, parentKey)
		if key == "" {
			return fmt.Errorf("failed to create sub-task for %q", t.ID)
		}
		p.TaskList[i].JiraKey = key
		keyByID[t.ID] = key
		slog.Info("arch: created subtask", "parent", parentKey, "id", t.ID, "jira", key)
	}
	for i, w := range p.Waves {
		for j, ref := range w.Tasks {
			if ref.JiraKey == "" {
				p.Waves[i].Tasks[j].JiraKey = keyByID[ref.ID]
			}
		}
	}

	*stage = "save-plan"
	if err := db.SavePlan(ctx, p); err != nil {
		return fmt.Errorf("save plan: %w", err)
	}

	*stage = "transition-subtask-in-progress"
	if sip := status.TaskJiraName(status.SubtaskInProgress); sip != "" {
		client.Transition(parentKey, sip)
	}

	*stage = "assign-wave-0"
	return assignWave(ctx, p, 0)
}

// AdvanceWave advances or finalises the plan for parentKey.
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
		return archiveDone(ctx, client, p)
	}

	active := p.Waves[p.ActiveWaveIdx]
	keys := keysOf(active)
	if len(keys) == 0 {
		p.ActiveWaveIdx++
		_ = db.SavePlan(ctx, p)
		return AdvanceWave(ctx, parentKey)
	}
	issues := client.GetTasksStatus(keys)
	if !allDone(issues, keys) {
		slog.Info("arch: wave still in progress", "key", parentKey, "wave", p.ActiveWaveIdx)
		return nil
	}

	p.ActiveWaveIdx++
	if err := db.SavePlan(ctx, p); err != nil {
		return err
	}
	if p.ActiveWaveIdx >= len(p.Waves) {
		return archiveDone(ctx, client, p)
	}
	return assignWave(ctx, p, p.ActiveWaveIdx)
}

func assignWave(_ context.Context, p *data.Plan, idx int) error {
	client := jira.Shared()
	if client == nil {
		return errors.New("jira client not initialised")
	}
	cfg := config.Get()
	if cfg == nil {
		return errors.New("config not loaded")
	}
	if idx < 0 || idx >= len(p.Waves) {
		return fmt.Errorf("wave idx %d out of range", idx)
	}
	for _, ref := range p.Waves[idx].Tasks {
		key := ref.JiraKey
		if key == "" {
			slog.Warn("arch: wave ref missing jira key", "ref", ref.ID, "parent", p.ParentJiraKey)
			continue
		}
		if !client.Assign(key, cfg.Jira.DeveloperJiraID) {
			slog.Error("arch: assign failed", "key", key)
		}
	}
	slog.Info("arch: wave assigned", "parent", p.ParentJiraKey, "wave", idx, "count", len(p.Waves[idx].Tasks))
	return nil
}

func archiveDone(ctx context.Context, client *jira.Client, p *data.Plan) error {
	done := status.TaskJiraName(status.Done)
	if done != "" {
		if !client.Transition(p.ParentJiraKey, done) {
			return fmt.Errorf("failed to transition parent %s to %s", p.ParentJiraKey, done)
		}
	}
	if err := db.MarkPlanDone(ctx, p.ParentJiraKey); err != nil {
		slog.Warn("arch: failed to mark plan done", "key", p.ParentJiraKey, "err", err)
	}
	_ = os.RemoveAll(config.WorkspacePath(p.ParentJiraKey))
	for _, t := range p.TaskList {
		if t.JiraKey != "" {
			_ = os.RemoveAll(config.WorkspacePath(t.JiraKey))
		}
	}
	slog.Info("arch: parent done, plan archived (DB retained)", "key", p.ParentJiraKey)
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
// unblocks successors the same way a merged one does.
func allDone(issues map[string]status.IssueInfo, keys []string) bool {
	for _, k := range keys {
		info, ok := issues[k]
		if !ok {
			return false
		}
		switch status.SubtaskCanonical(info.Status) {
		case status.Done, status.Dismissed:
			continue
		default:
			return false
		}
	}
	return true
}

type rawPlan struct {
	TaskList []data.PlannedTask `json:"task_list"`
	Waves    []data.Wave        `json:"waves"`
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
