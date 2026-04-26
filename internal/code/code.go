// Package code drives a single sub-task through a GitHub PR.
package code

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/data"
	"github.com/randheer094/velocity/internal/db"
	"github.com/randheer094/velocity/internal/git"
	"github.com/randheer094/velocity/internal/github"
	"github.com/randheer094/velocity/internal/jira"
	"github.com/randheer094/velocity/internal/llm"
	"github.com/randheer094/velocity/internal/status"
)

var (
	inFlight   = map[string]struct{}{}
	inFlightMu sync.Mutex
)

// Test seams: overridden by tests via code/main_test.go.
var (
	parseRepoURL        = github.ParseRepoURL
	configureAuthRemote = git.ConfigureAuthenticatedRemote
)

func claim(key string) bool {
	inFlightMu.Lock()
	defer inFlightMu.Unlock()
	if _, ok := inFlight[key]; ok {
		return false
	}
	inFlight[key] = struct{}{}
	return true
}

func release(key string) {
	inFlightMu.Lock()
	delete(inFlight, key)
	inFlightMu.Unlock()
}

// Run executes one sub-task end-to-end. Invoke via webhook.Enqueue.
func Run(ctx context.Context, issueKey, parentKey, repoURL, title, description string) {
	if !claim(issueKey) {
		slog.Info("code: already in flight", "key", issueKey)
		return
	}
	defer release(issueKey)

	runCtx, cancel := context.WithCancel(ctx)
	registerCancel(issueKey, cancel)
	defer func() {
		unregisterCancel(issueKey)
		cancel()
	}()

	stage := "init"
	defer func() {
		if r := recover(); r != nil {
			recordFailure(runCtx, issueKey, parentKey, repoURL, title, "panic", fmt.Errorf("%v", r))
		}
	}()

	if err := code(runCtx, issueKey, parentKey, repoURL, title, description, &stage); err != nil {
		recordFailure(runCtx, issueKey, parentKey, repoURL, title, stage, err)
	}
}

func code(ctx context.Context, issueKey, parentKey, repoURL, title, description string, stage *string) error {
	*stage = "load-config"
	cfg := config.Get()
	if cfg == nil {
		return errors.New("config not loaded")
	}
	jiraClient := jira.Shared()
	if jiraClient == nil {
		return errors.New("jira client not initialised")
	}

	*stage = "retry-guard"
	forcePush := false
	if existing, _ := db.GetCodeTask(ctx, issueKey); existing != nil {
		switch existing.Status {
		case data.CodeDone, data.CodeInReview:
			slog.Info("code: task already terminal or in review, ignoring re-assignment", "key", issueKey, "status", existing.Status)
			return nil
		case data.CodeCodingFailed:
			slog.Info("code: retrying prior failed task with force-with-lease", "key", issueKey)
			forcePush = true
		}
	}

	*stage = "save-task"
	task := &data.CodeTask{
		IssueKey:      issueKey,
		ParentJiraKey: parentKey,
		RepoURL:       repoURL,
		Title:         title,
		Description:   description,
		Branch:        issueKey,
		Status:        data.CodeCoding,
		JiraStatus:    status.SubtaskJiraName(status.Coding),
		CreatedAt:     time.Now().UTC(),
	}
	if err := db.SaveCodeTask(ctx, task); err != nil {
		return fmt.Errorf("save code task: %w", err)
	}

	*stage = "transition-coding"
	if coding := status.SubtaskJiraName(status.Coding); coding != "" {
		jiraClient.Transition(issueKey, coding)
	}

	*stage = "parse-repo-url"
	repoFullName, err := parseRepoURL(repoURL)
	if err != nil {
		return err
	}

	*stage = "clone"
	workspace := config.WorkspacePath(issueKey)
	_ = os.RemoveAll(workspace)
	if err := os.MkdirAll(config.WorkspaceDir, 0o755); err != nil {
		return err
	}
	if err := git.Clone(repoURL, workspace); err != nil {
		return fmt.Errorf("clone: %w", err)
	}
	if err := configureAuthRemote(workspace, repoFullName); err != nil {
		return fmt.Errorf("auth remote: %w", err)
	}

	*stage = "default-branch"
	baseBranch, err := git.DefaultBranch(workspace)
	if err != nil {
		return fmt.Errorf("default branch: %w", err)
	}

	*stage = "checkout-branch"
	if err := git.CheckoutNewBranch(workspace, issueKey); err != nil {
		return fmt.Errorf("branch: %w", err)
	}

	*stage = "code-llm"
	prompt := buildCodePrompt(issueKey, title, description)
	opts := llm.OptionsFromRoleConfig(cfg.LLM.Code, workspace)
	if _, err := llm.RunPrompt(ctx, prompt, opts); err != nil {
		return fmt.Errorf("code llm: %w", err)
	}

	*stage = "commit"
	commitMsg := fmt.Sprintf("%s: %s", issueKey, title)
	committed, err := git.AddAllAndCommit(workspace, commitMsg)
	if err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	if !committed {
		return errors.New("code agent produced no changes")
	}

	*stage = "push"
	if forcePush {
		if err := git.PushForceWithLease(workspace, issueKey); err != nil {
			return fmt.Errorf("push (force-with-lease): %w", err)
		}
	} else {
		if err := git.Push(workspace, issueKey); err != nil {
			return fmt.Errorf("push: %w", err)
		}
	}

	*stage = "open-pr"
	jiraURL := fmt.Sprintf("%s/browse/%s", cfg.Jira.BaseURL, issueKey)
	prURL := github.New().CreateOrUpdatePR(
		repoFullName,
		commitMsg,
		BuildPRBody(title, description, issueKey, jiraURL),
		issueKey,
		baseBranch,
	)
	if prURL == "" {
		return errors.New("failed to open PR")
	}

	*stage = "save-task-post-pr"
	task.PRURL = prURL
	task.Status = data.CodeInReview
	task.JiraStatus = status.SubtaskJiraName(status.InReview)
	task.Error = ""
	task.LastErrorStage = ""
	task.FailedAt = nil
	if err := db.SaveCodeTask(ctx, task); err != nil {
		return fmt.Errorf("save code task (post-pr): %w", err)
	}

	*stage = "transition-in-review"
	if task.JiraStatus != "" {
		jiraClient.Transition(issueKey, task.JiraStatus)
	}

	slog.Info("code: PR open", "key", issueKey, "url", prURL)
	return nil
}

// MarkMerged transitions the sub-task to DONE after its PR merges.
// Workspace cleanup is enqueued as a separate Cleanup event — one
// logical step per queue row.
func MarkMerged(ctx context.Context, issueKey, prURL string) error {
	client := jira.Shared()
	if client == nil {
		return errors.New("jira client not initialised")
	}
	done := status.SubtaskJiraName(status.Done)
	if done == "" {
		return errors.New("subtask DONE status not configured")
	}
	if !client.Transition(issueKey, done) {
		return fmt.Errorf("failed to transition %s to %s", issueKey, done)
	}
	task, _ := db.GetCodeTask(ctx, issueKey)
	if task != nil {
		task.Status = data.CodeDone
		task.JiraStatus = done
		if prURL != "" {
			task.PRURL = prURL
		}
		_ = db.SaveCodeTask(ctx, task)
	}
	EnqueueFn(kindCleanup, "code.Cleanup:"+issueKey,
		map[string]any{"issue_key": issueKey})
	slog.Info("code: merged → done", "key", issueKey)
	return nil
}

// OnDismissed cancels any in-flight run and marks the sub-task
// dismissed. The caller must enqueue arch.AdvanceWave so the parent
// advances past it. jiraStatus is the sub-task's Jira status name
// from the dismiss webhook. Workspace cleanup is enqueued as a
// separate Cleanup event so the cancelled Run has time to release
// its file descriptors.
func OnDismissed(ctx context.Context, issueKey, jiraStatus string) error {
	cancelIfRunning(issueKey)
	if err := db.MarkCodeDismissed(ctx, issueKey, jiraStatus); err != nil {
		slog.Warn("code: mark dismissed", "key", issueKey, "err", err)
	}
	EnqueueFn(kindCleanup, "code.Cleanup:"+issueKey,
		map[string]any{"issue_key": issueKey})
	return nil
}

// Cleanup removes the per-issue workspace. Skipped when a Run or
// Iterate is still in flight for the same key — its pre-clone
// RemoveAll handles eventual cleanup on the next Run.
func Cleanup(_ context.Context, issueKey string) error {
	if IsInFlight(issueKey) {
		slog.Warn("code: run in flight, skipping workspace cleanup", "key", issueKey)
		return nil
	}
	_ = os.RemoveAll(config.WorkspacePath(issueKey))
	slog.Info("code: workspace cleaned", "key", issueKey)
	return nil
}
