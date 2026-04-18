package code

import (
	"context"
	"testing"

	"github.com/randheer094/velocity/internal/data"
	"github.com/randheer094/velocity/internal/db"
)

func TestRunDuplicateClaim(t *testing.T) {
	requireDB(t)
	if !claim("CODE-RUN-CLAIM") {
		t.Fatal("first claim should succeed")
	}
	defer release("CODE-RUN-CLAIM")
	Run(context.Background(), "CODE-RUN-CLAIM", "P-1", "https://x", "t", "d")
}

func TestRunBadRepoFails(t *testing.T) {
	requireDB(t)
	// Invalid repo URL → ParseRepoURL fails → recordFailure called.
	Run(context.Background(), "CODE-RUN-FAIL", "P-1", "not-a-url", "t", "d")
	got, _ := db.GetCodeTask(context.Background(), "CODE-RUN-FAIL")
	if got == nil || got.Status != data.CodeFailed {
		t.Errorf("got = %+v", got)
	}
}

func TestRunRetryGuardTerminalIgnored(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	task := &data.CodeTask{
		IssueKey:      "CODE-RUN-DONE",
		ParentJiraKey: "P-1",
		RepoURL:       "r",
		Title:         "x",
		Branch:        "CODE-RUN-DONE",
		Status:        data.CodeDone,
	}
	if err := db.SaveCodeTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	Run(ctx, "CODE-RUN-DONE", "P-1", "r", "x", "d")
	got, _ := db.GetCodeTask(ctx, "CODE-RUN-DONE")
	if got.Status != data.CodeDone {
		t.Errorf("status changed: %q", got.Status)
	}
}

func TestRunRetryGuardFailedRetries(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	if err := db.MarkCodeFailed(ctx, "CODE-RUN-RETRY", "P-1", "not-a-url", "x", "CODE-RUN-RETRY", "stage", "boom"); err != nil {
		t.Fatal(err)
	}
	// Re-running picks the failed task and tries to retry → ParseRepoURL fails again.
	Run(ctx, "CODE-RUN-RETRY", "P-1", "not-a-url", "x", "d")
	got, _ := db.GetCodeTask(ctx, "CODE-RUN-RETRY")
	if got.Status != data.CodeFailed {
		t.Errorf("expected failed: %+v", got)
	}
}

func TestRunFullCodeSucceeds(t *testing.T) {
	requireDB(t)
	remote := setupBareRemote(t)

	// parseRepoURL must accept the local bare path; restore after.
	prev := parseRepoURL
	parseRepoURL = func(string) (string, error) { return "owner/repo", nil }
	defer func() { parseRepoURL = prev }()

	ctx := context.Background()
	Run(ctx, "CODE-RUN-OK", "P-1", remote, "implement feature", "do the thing")

	got, _ := db.GetCodeTask(ctx, "CODE-RUN-OK")
	if got == nil {
		t.Fatal("task not saved")
	}
	if got.Status != data.CodePROpen {
		t.Errorf("status = %q, want %q (err=%q stage=%q)", got.Status, data.CodePROpen, got.Error, got.LastErrorStage)
	}
	if got.PRURL == "" {
		t.Error("PRURL empty")
	}
}

func TestRunNoChangesFails(t *testing.T) {
	requireDB(t)
	remote := setupBareRemote(t)

	prev := parseRepoURL
	parseRepoURL = func(string) (string, error) { return "owner/repo", nil }
	defer func() { parseRepoURL = prev }()

	t.Setenv("CODE_TEST_NO_WRITE", "1")
	ctx := context.Background()
	Run(ctx, "CODE-RUN-NOOP", "P-1", remote, "title", "desc")

	got, _ := db.GetCodeTask(ctx, "CODE-RUN-NOOP")
	if got == nil || got.Status != data.CodeFailed {
		t.Errorf("expected failed: %+v", got)
	}
	if got != nil && got.LastErrorStage != "commit" {
		t.Errorf("stage = %q, want commit", got.LastErrorStage)
	}
}

func TestRunRetryGuardPROpenIgnored(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	task := &data.CodeTask{
		IssueKey:      "CODE-RUN-PROPEN",
		ParentJiraKey: "P-1",
		RepoURL:       "r",
		Title:         "x",
		Branch:        "CODE-RUN-PROPEN",
		Status:        data.CodePROpen,
	}
	if err := db.SaveCodeTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	Run(ctx, "CODE-RUN-PROPEN", "P-1", "r", "x", "d")
	got, _ := db.GetCodeTask(ctx, "CODE-RUN-PROPEN")
	if got.Status != data.CodePROpen {
		t.Errorf("status changed: %q", got.Status)
	}
}

func TestRunFullCodeForcePush(t *testing.T) {
	requireDB(t)
	remote := setupBareRemote(t)

	prev := parseRepoURL
	parseRepoURL = func(string) (string, error) { return "owner/repo", nil }
	defer func() { parseRepoURL = prev }()

	ctx := context.Background()
	// Pre-mark failed so the retry guard sets forcePush=true.
	if err := db.MarkCodeFailed(ctx, "CODE-RUN-FORCE", "P-1", remote, "x", "CODE-RUN-FORCE", "stage", "boom"); err != nil {
		t.Fatal(err)
	}
	Run(ctx, "CODE-RUN-FORCE", "P-1", remote, "implement", "desc")

	got, _ := db.GetCodeTask(ctx, "CODE-RUN-FORCE")
	if got == nil || got.Status != data.CodePROpen {
		t.Errorf("got = %+v", got)
	}
}
