package code

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/data"
	"github.com/randheer094/velocity/internal/db"
	"github.com/randheer094/velocity/internal/git"
	"github.com/randheer094/velocity/internal/github"
	"github.com/randheer094/velocity/internal/jira"
	"github.com/randheer094/velocity/internal/llm"
)

// IterateReason identifies what triggered an iterate run. The webhook
// handlers set this so failures can be reported back on the right
// surface (Jira comment for CI, PR comment for /velocity).
type IterateReason string

const (
	IterateCI      IterateReason = "ci-failure"
	IterateCommand IterateReason = "pr-command"
)

// Iterate refreshes an open PR branch on behalf of a GitHub event (CI
// failure or `/velocity` comment). The PR does not need to have been
// opened by velocity: iterate fresh-clones the repo, checks out
// `branch`, and lets the LLM rebase onto the default branch, resolve
// any conflicts, and apply the follow-up request before the runner
// commits and force-pushes. If a velocity `code_tasks` row exists for
// the branch, its title/description seed the prompt for context.
//
// extraInstruction is appended to the code prompt so the LLM has the
// CI failure log or the user's /velocity command as fresh context.
// commitHint becomes the trailing segment of the commit subject
// ("<branch>: <hint>"). Empty hint falls back to "iterate".
func Iterate(ctx context.Context, repoURL, branch, prURL string, reason IterateReason, extraInstruction, commitHint string) {
	if !claim(branch) {
		slog.Info("code: iterate already in flight", "branch", branch)
		return
	}
	defer release(branch)

	runCtx, cancel := context.WithCancel(ctx)
	registerCancel(branch, cancel)
	defer func() {
		unregisterCancel(branch)
		cancel()
	}()

	stage := "init"
	defer func() {
		if r := recover(); r != nil {
			reportIterateFailure(branch, prURL, reason, "panic", fmt.Errorf("%v", r))
		}
	}()

	if err := iterate(runCtx, repoURL, branch, prURL, extraInstruction, commitHint, &stage); err != nil {
		reportIterateFailure(branch, prURL, reason, stage, err)
	}
}

func iterate(ctx context.Context, repoURL, branch, prURL, extra, commitHint string, stage *string) error {
	*stage = "load-config"
	cfg := config.Get()
	if cfg == nil {
		return errors.New("config not loaded")
	}
	if repoURL == "" {
		return errors.New("empty repo url")
	}
	if branch == "" {
		return errors.New("empty branch")
	}

	*stage = "parse-repo-url"
	repoFullName, err := parseRepoURL(repoURL)
	if err != nil {
		return err
	}

	var title, description string
	if task, _ := db.GetCodeTask(ctx, branch); task != nil {
		title = task.Title
		description = task.Description
	}

	*stage = "prepare-workspace"
	workspace := config.WorkspacePath(branch)
	if err := os.RemoveAll(workspace); err != nil {
		return fmt.Errorf("remove workspace: %w", err)
	}
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
	if err := git.CheckoutBranch(workspace, branch); err != nil {
		return fmt.Errorf("checkout %s: %w", branch, err)
	}

	*stage = "code-llm"
	prompt := buildIteratePrompt(branch, title, description, baseBranch, extra)
	opts := llm.OptionsFromRoleConfig(cfg.LLM.Code, workspace)
	if _, err := llm.RunPrompt(ctx, prompt, opts); err != nil {
		return fmt.Errorf("code llm: %w", err)
	}

	*stage = "commit"
	commitMsg := fmt.Sprintf("%s: iterate", branch)
	if h := strings.TrimSpace(commitHint); h != "" {
		commitMsg = fmt.Sprintf("%s: %s", branch, h)
	}
	committed, err := git.AddAllAndCommit(workspace, commitMsg)
	if err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	if !committed {
		// The LLM may have committed its own edits (e.g. during rebase
		// conflict resolution). Force-push the branch regardless.
		slog.Info("code: iterate produced no uncommitted edits; pushing branch", "branch", branch)
	}

	*stage = "push"
	if err := git.PushForceWithLease(workspace, branch); err != nil {
		return fmt.Errorf("push (force-with-lease): %w", err)
	}

	slog.Info("code: iterate pushed", "branch", branch, "extra", truncate(extra, 80))
	return nil
}

func reportIterateFailure(branch, prURL string, reason IterateReason, stage string, err error) {
	msg := redactAndTruncate(err.Error())
	slog.Error("code: iterate failed", "branch", branch, "stage", stage, "reason", reason, "err", err)

	// Jira comment only makes sense when the branch is a Jira issue key.
	if data.ValidJiraKey(branch) {
		if client := jira.Shared(); client != nil {
			_ = client.CommentIssue(branch, formatIterateJiraComment(string(reason), stage, msg))
		}
	}

	if prURL != "" {
		if repo, num := parsePRURL(prURL); repo != "" && num > 0 {
			github.New().AddPRComment(repo, num, formatIteratePRComment(stage, msg))
		}
	}
}

// parsePRURL pulls "owner/repo" + PR number out of a standard
// https://github.com/<owner>/<repo>/pull/<num> URL. Returns zeros on
// any shape we don't recognise.
func parsePRURL(prURL string) (string, int) {
	u := strings.TrimSpace(prURL)
	u = strings.TrimPrefix(u, "https://github.com/")
	u = strings.TrimPrefix(u, "http://github.com/")
	parts := strings.Split(u, "/")
	if len(parts) < 4 || parts[2] != "pull" {
		return "", 0
	}
	num, err := strconv.Atoi(parts[3])
	if err != nil {
		return "", 0
	}
	return parts[0] + "/" + parts[1], num
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
