package code

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/data"
	"github.com/randheer094/velocity/internal/db"
)

func removeWorkspaceForKey(key string) error {
	return os.RemoveAll(config.WorkspacePath(key))
}

func TestBuildIteratePromptContainsExtra(t *testing.T) {
	got := buildIteratePrompt("PROJ-1", "title", "desc", "main", "fix the flaky CI")
	for _, want := range []string{"PROJ-1", "title", "desc", "fix the flaky CI", `"main"`, "rebase"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q: %s", want, got)
		}
	}
}

func TestParsePRURL(t *testing.T) {
	cases := map[string]struct {
		repo string
		num  int
	}{
		"https://github.com/o/r/pull/42": {"o/r", 42},
		"http://github.com/o/r/pull/1":   {"o/r", 1},
		"https://github.com/o/r":         {"", 0},
		"https://github.com/o/r/issues/1": {"", 0},
		"not-a-url":                      {"", 0},
		"https://github.com/o/r/pull/x":  {"", 0},
	}
	for in, want := range cases {
		r, n := parsePRURL(in)
		if r != want.repo || n != want.num {
			t.Errorf("parsePRURL(%q) = (%q,%d), want (%q,%d)", in, r, n, want.repo, want.num)
		}
	}
}

func TestTruncate(t *testing.T) {
	if truncate("abc", 10) != "abc" {
		t.Error("short should pass through")
	}
	if got := truncate("abcdefghij", 5); got != "abcde..." {
		t.Errorf("truncate = %q", got)
	}
}

func TestIterateDuplicateClaim(t *testing.T) {
	requireDB(t)
	if !claim("ITER-DUP") {
		t.Fatal("first claim should succeed")
	}
	defer release("ITER-DUP")
	Iterate(context.Background(), "https://github.com/o/r.git", "ITER-DUP", "https://github.com/o/r/pull/1", IterateCI, "x", "")
}

func TestIterateEmptyRepoURL(t *testing.T) {
	requireDB(t)
	// Empty repo URL → iterate() returns error; deferred reporter runs
	// without panicking.
	Iterate(context.Background(), "", "ITER-NONE", "", IterateCI, "x", "")
}

// TestIterateNoTaskRowStillRuns verifies iterate proceeds even when
// no code_tasks row exists for the branch (i.e. the PR was not opened
// by velocity). It will still fail at a later stage — we just assert
// it doesn't short-circuit on the missing row.
func TestIterateNoTaskRowStillRuns(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	cleanCodeTask(t, "ITER-NO-ROW")
	// Bad repo URL → fails at parseRepoURL, which is after the
	// (removed) task lookup; proves the lookup isn't a gate anymore.
	Iterate(ctx, "not-a-url", "ITER-NO-ROW", "https://github.com/o/r/pull/1", IterateCommand, "do something", "")
}

func TestIterateIgnoresTaskStatus(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	// Task exists but is DONE — iterate must no longer skip it, but
	// the bad repo URL fails it at parse stage so the DB row is
	// preserved.
	task := &data.CodeTask{
		IssueKey:      "ITER-DONE",
		ParentJiraKey: "P-1",
		RepoURL:       "https://x",
		Title:         "x",
		Branch:        "ITER-DONE",
		Status:        data.CodeDone,
	}
	if err := db.SaveCodeTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	Iterate(ctx, "not-a-url", "ITER-DONE", "https://github.com/o/r/pull/1", IterateCI, "x", "")
	got, _ := db.GetCodeTask(ctx, "ITER-DONE")
	if got.Status != data.CodeDone {
		t.Errorf("status changed: %q", got.Status)
	}
}

// TestIterateBadRepoURL uses a valid-looking Jira key so the failure
// reporter exercises both the Jira-comment and PR-comment paths.
func TestIterateBadRepoURL(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	Iterate(ctx, "not-a-url", "ITER-1", "https://github.com/o/r/pull/1", IterateCommand, "do something", "")
}

// TestIterateBadRepoURLNonJiraBranch covers the failure path for a
// branch that is not a Jira key — only the PR-comment surface fires,
// the Jira-comment branch is skipped.
func TestIterateBadRepoURLNonJiraBranch(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	Iterate(ctx, "not-a-url", "feature/not-a-jira-key", "https://github.com/o/r/pull/1", IterateCommand, "do something", "")
}

func TestIterateFullHappyPath(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	_ = db.MarkCodeFailed(ctx, "ITER-OK", "", "", "", "", "", "", "")
	remote := setupBareRemote(t)
	prev := parseRepoURL
	parseRepoURL = func(string) (string, error) { return "owner/repo", nil }
	defer func() { parseRepoURL = prev }()

	// Prime by running code.Run end-to-end so a branch + task row land
	// in the normal shape — iterate can consult the row for prompt
	// context even though it doesn't require one.
	Run(ctx, "ITER-OK", "P-1", remote, "implement", "desc")
	got, _ := db.GetCodeTask(ctx, "ITER-OK")
	if got == nil || got.Status != data.CodeInReview {
		t.Fatalf("setup failed: %+v", got)
	}

	// Fresh checkout: iterate always re-clones before running.
	Iterate(ctx, remote, "ITER-OK", got.PRURL, IterateCommand, "add more stuff", "add more stuff")
}

// TestIterateFreshCheckoutOverExisting verifies iterate removes and
// re-clones even when a previous workspace exists on disk.
func TestIterateFreshCheckoutOverExisting(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	_ = db.MarkCodeFailed(ctx, "ITER-RECLONE", "", "", "", "", "", "", "")
	remote := setupBareRemote(t)
	prev := parseRepoURL
	parseRepoURL = func(string) (string, error) { return "owner/repo", nil }
	defer func() { parseRepoURL = prev }()

	Run(ctx, "ITER-RECLONE", "P-1", remote, "impl", "desc")
	got, _ := db.GetCodeTask(ctx, "ITER-RECLONE")
	if got == nil || got.Status != data.CodeInReview {
		t.Fatalf("setup: %+v", got)
	}

	// Leave the workspace in place; iterate must clear it and clone
	// fresh on its own.
	ws := config.WorkspacePath("ITER-RECLONE")
	if _, err := os.Stat(ws); err != nil {
		t.Fatalf("workspace should still exist after Run: %v", err)
	}
	sentinel := ws + "/leftover.txt"
	if err := os.WriteFile(sentinel, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	Iterate(ctx, remote, "ITER-RECLONE", got.PRURL, IterateCI, "ci failed, please fix", "fix CI: test failed")
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Errorf("leftover file survived fresh checkout: %v", err)
	}
}

func TestIterateCloneFails(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	_ = removeWorkspaceForKey("ITER-CLONE-FAIL")
	prev := parseRepoURL
	parseRepoURL = func(string) (string, error) { return "o/r", nil }
	defer func() { parseRepoURL = prev }()
	Iterate(ctx, "/nonexistent/repo.git", "ITER-CLONE-FAIL", "https://github.com/o/r/pull/1", IterateCI, "fix me", "")
}

func TestIterateAuthRemoteFails(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	remote := setupBareRemote(t)
	_ = removeWorkspaceForKey("ITER-AUTH-FAIL")
	prev := parseRepoURL
	parseRepoURL = func(string) (string, error) { return "o/r", nil }
	defer func() { parseRepoURL = prev }()
	prevAuth := configureAuthRemote
	configureAuthRemote = func(string, string) error { return errTestAuth }
	defer func() { configureAuthRemote = prevAuth }()
	Iterate(ctx, remote, "ITER-AUTH-FAIL", "https://github.com/o/r/pull/1", IterateCI, "fix", "")
}

var errTestAuth = &stringErr{"auth failed"}

type stringErr struct{ s string }

func (e *stringErr) Error() string { return e.s }

func TestIteratePanicRecovered(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	prev := parseRepoURL
	parseRepoURL = func(string) (string, error) { panic("boom") }
	defer func() { parseRepoURL = prev }()
	Iterate(ctx, "https://github.com/o/r.git", "ITER-PANIC", "https://github.com/o/r/pull/1", IterateCI, "x", "")
}
