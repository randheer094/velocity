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

// Iterate updates an existing sub-task branch in place: fetch, reset
// to origin, rebase onto default branch, run the LLM with extra
// instruction, commit, and force-push-with-lease. The sub-task must
// already be in_review and have a recorded PR URL.
//
// extraInstruction is appended to the code prompt so the LLM has the
// CI failure log or the user's /velocity command as fresh context.
func Iterate(ctx context.Context, issueKey string, reason IterateReason, extraInstruction string) {
	if !claim(issueKey) {
		slog.Info("code: iterate already in flight", "key", issueKey)
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
			reportIterateFailure(runCtx, issueKey, reason, "panic", fmt.Errorf("%v", r))
		}
	}()

	if err := iterate(runCtx, issueKey, extraInstruction, &stage); err != nil {
		reportIterateFailure(runCtx, issueKey, reason, stage, err)
	}
}

func iterate(ctx context.Context, issueKey, extra string, stage *string) error {
	*stage = "load-config"
	cfg := config.Get()
	if cfg == nil {
		return errors.New("config not loaded")
	}

	*stage = "load-task"
	task, err := db.GetCodeTask(ctx, issueKey)
	if err != nil {
		return fmt.Errorf("load code task: %w", err)
	}
	if task == nil {
		return errors.New("no code task for key")
	}
	if task.Status != data.CodeInReview {
		slog.Info("code: iterate ignored, task not in review", "key", issueKey, "status", task.Status)
		return nil
	}

	*stage = "parse-repo-url"
	repoFullName, err := parseRepoURL(task.RepoURL)
	if err != nil {
		return err
	}

	*stage = "prepare-workspace"
	workspace := config.WorkspacePath(issueKey)
	if !git.WorkspaceExists(workspace) {
		_ = os.RemoveAll(workspace)
		if err := os.MkdirAll(config.WorkspaceDir, 0o755); err != nil {
			return err
		}
		if err := git.Clone(task.RepoURL, workspace); err != nil {
			return fmt.Errorf("clone: %w", err)
		}
	}
	if err := configureAuthRemote(workspace, repoFullName); err != nil {
		return fmt.Errorf("auth remote: %w", err)
	}

	*stage = "fetch"
	if err := git.FetchAll(workspace); err != nil {
		return fmt.Errorf("fetch: %w", err)
	}

	*stage = "default-branch"
	baseBranch, err := git.DefaultBranch(workspace)
	if err != nil {
		return fmt.Errorf("default branch: %w", err)
	}

	*stage = "checkout-branch"
	if err := git.CheckoutBranch(workspace, issueKey); err != nil {
		if err := git.CheckoutNewBranch(workspace, issueKey); err != nil {
			return fmt.Errorf("checkout: %w", err)
		}
	}

	*stage = "sync-to-remote"
	if err := git.ResetHardToRemote(workspace, issueKey); err != nil {
		return fmt.Errorf("reset to remote branch: %w", err)
	}

	*stage = "rebase-main"
	if err := git.RebaseOnto(workspace, baseBranch); err != nil {
		return fmt.Errorf("rebase onto %s: %w", baseBranch, err)
	}

	*stage = "code-llm"
	prompt := buildIteratePrompt(issueKey, task.Title, task.Description, extra)
	opts := llm.OptionsFromRoleConfig(cfg.LLM.Code, workspace)
	if _, err := llm.RunPrompt(ctx, prompt, opts); err != nil {
		return fmt.Errorf("code llm: %w", err)
	}

	*stage = "commit"
	commitMsg := fmt.Sprintf("%s: iterate", issueKey)
	committed, err := git.AddAllAndCommit(workspace, commitMsg)
	if err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	if !committed {
		// Rebase alone may have resolved things (common for CI fixes
		// caused by a merge to main). Force-push the rebased branch.
		slog.Info("code: iterate produced no edits; pushing rebased branch", "key", issueKey)
	}

	*stage = "push"
	if err := git.PushForceWithLease(workspace, issueKey); err != nil {
		return fmt.Errorf("push (force-with-lease): %w", err)
	}

	slog.Info("code: iterate pushed", "key", issueKey, "extra", truncate(extra, 80))
	return nil
}

func reportIterateFailure(ctx context.Context, issueKey string, reason IterateReason, stage string, err error) {
	msg := redactAndTruncate(err.Error())
	slog.Error("code: iterate failed", "key", issueKey, "stage", stage, "reason", reason, "err", err)

	if client := jira.Shared(); client != nil {
		_ = client.CommentIssue(issueKey, fmt.Sprintf(
			"Velocity iterate (%s) failed at stage *%s*.\n\n```\n%s\n```\n\nSee daemon.log for full details.",
			reason, stage, msg,
		))
	}

	// Best-effort PR comment so operators see the failure where they
	// triggered it. Needs the PR URL from the DB row.
	task, _ := db.GetCodeTask(ctx, issueKey)
	if task != nil && task.PRURL != "" {
		if repo, num := parsePRURL(task.PRURL); repo != "" && num > 0 {
			github.New().AddPRComment(repo, num,
				fmt.Sprintf("Velocity could not complete the requested action (stage `%s`):\n\n```\n%s\n```",
					stage, msg))
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
