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
	got := buildIteratePrompt("PROJ-1", "title", "desc", "fix the flaky CI")
	for _, want := range []string{"PROJ-1", "title", "desc", "fix the flaky CI"} {
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
	Iterate(context.Background(), "ITER-DUP", IterateCI, "x")
}

func TestIterateNoTask(t *testing.T) {
	requireDB(t)
	// No row in DB → iterate() returns "no code task" error; deferred
	// reporter runs without panicking.
	Iterate(context.Background(), "ITER-NONE", IterateCI, "x")
}

func TestIterateSkipsWhenNotInReview(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	task := &data.CodeTask{
		IssueKey:      "ITER-NOT-IR",
		ParentJiraKey: "P-1",
		RepoURL:       "https://x",
		Title:         "x",
		Branch:        "ITER-NOT-IR",
		Status:        data.CodeDone,
	}
	if err := db.SaveCodeTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	Iterate(ctx, "ITER-NOT-IR", IterateCI, "x")
	got, _ := db.GetCodeTask(ctx, "ITER-NOT-IR")
	if got.Status != data.CodeDone {
		t.Errorf("status changed: %q", got.Status)
	}
}

func TestIterateBadRepoURL(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	task := &data.CodeTask{
		IssueKey:      "ITER-BAD-URL",
		ParentJiraKey: "P-1",
		RepoURL:       "not-a-url",
		Title:         "x",
		Branch:        "ITER-BAD-URL",
		PRURL:         "https://github.com/o/r/pull/1",
		Status:        data.CodeInReview,
	}
	if err := db.SaveCodeTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	Iterate(ctx, "ITER-BAD-URL", IterateCommand, "do something")
	// Failure reporter ran; status should remain in_review.
	got, _ := db.GetCodeTask(ctx, "ITER-BAD-URL")
	if got.Status != data.CodeInReview {
		t.Errorf("status changed: %q", got.Status)
	}
}

func TestIterateFullHappyPath(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	// DB persists across test-db.sh runs; force a fresh task state so
	// Run() re-clones instead of short-circuiting on a prior in_review.
	_ = db.MarkCodeFailed(ctx, "ITER-OK", "", "", "", "", "", "", "")
	remote := setupBareRemote(t)
	prev := parseRepoURL
	parseRepoURL = func(string) (string, error) { return "owner/repo", nil }
	defer func() { parseRepoURL = prev }()

	// Prime by running code.Run end-to-end so the workspace + branch + PR
	// row land in the normal shape.
	Run(ctx, "ITER-OK", "P-1", remote, "implement", "desc")
	got, _ := db.GetCodeTask(ctx, "ITER-OK")
	if got == nil || got.Status != data.CodeInReview {
		t.Fatalf("setup failed: %+v", got)
	}

	// Now iterate. LLM stub writes implementation.go again, which is a
	// no-op on an unchanged tree — Iterate treats that as OK and still
	// force-pushes the rebased branch.
	Iterate(ctx, "ITER-OK", IterateCommand, "add more stuff")
	after, _ := db.GetCodeTask(ctx, "ITER-OK")
	if after.Status != data.CodeInReview {
		t.Errorf("status should remain in_review: %q", after.Status)
	}
}

func TestIterateReclonesMissingWorkspace(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	// Prime + Run to push branch upstream.
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

	// Blow away the workspace so iterate has to re-clone.
	if err := removeWorkspaceForKey("ITER-RECLONE"); err != nil {
		t.Fatal(err)
	}

	Iterate(ctx, "ITER-RECLONE", IterateCI, "ci failed, please fix")
	after, _ := db.GetCodeTask(ctx, "ITER-RECLONE")
	if after.Status != data.CodeInReview {
		t.Errorf("status = %q", after.Status)
	}
}

func TestIterateCloneFails(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	// Seed a task pointing at a non-existent repo, with no workspace
	// on disk → iterate tries to clone and fails at the clone stage.
	task := &data.CodeTask{
		IssueKey:      "ITER-CLONE-FAIL",
		ParentJiraKey: "P-1",
		RepoURL:       "/nonexistent/repo.git",
		Title:         "x",
		Branch:        "ITER-CLONE-FAIL",
		Status:        data.CodeInReview,
	}
	if err := db.SaveCodeTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	_ = removeWorkspaceForKey("ITER-CLONE-FAIL")
	prev := parseRepoURL
	parseRepoURL = func(string) (string, error) { return "o/r", nil }
	defer func() { parseRepoURL = prev }()
	Iterate(ctx, "ITER-CLONE-FAIL", IterateCI, "fix me")
}

func TestIterateFetchFailsOnBrokenWorkspace(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	task := &data.CodeTask{
		IssueKey:      "ITER-FETCH-FAIL",
		ParentJiraKey: "P-1",
		RepoURL:       "https://github.com/o/r.git",
		Title:         "x",
		Branch:        "ITER-FETCH-FAIL",
		Status:        data.CodeInReview,
	}
	if err := db.SaveCodeTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	// Create a workspace dir with a .git subdir so WorkspaceExists
	// returns true, but no remote — FetchAll will error.
	ws := config.WorkspacePath("ITER-FETCH-FAIL")
	_ = os.RemoveAll(ws)
	if err := os.MkdirAll(ws+"/.git", 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(ws) })
	prev := parseRepoURL
	parseRepoURL = func(string) (string, error) { return "o/r", nil }
	defer func() { parseRepoURL = prev }()
	prevAuth := configureAuthRemote
	configureAuthRemote = func(string, string) error { return nil }
	defer func() { configureAuthRemote = prevAuth }()
	Iterate(ctx, "ITER-FETCH-FAIL", IterateCI, "fix me")
}

func TestIterateAuthRemoteFails(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	task := &data.CodeTask{
		IssueKey:      "ITER-AUTH-FAIL",
		ParentJiraKey: "P-1",
		RepoURL:       "https://github.com/o/r.git",
		Title:         "x",
		Branch:        "ITER-AUTH-FAIL",
		Status:        data.CodeInReview,
	}
	if err := db.SaveCodeTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	ws := config.WorkspacePath("ITER-AUTH-FAIL")
	_ = os.RemoveAll(ws)
	if err := os.MkdirAll(ws+"/.git", 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(ws) })
	prev := parseRepoURL
	parseRepoURL = func(string) (string, error) { return "o/r", nil }
	defer func() { parseRepoURL = prev }()
	prevAuth := configureAuthRemote
	configureAuthRemote = func(string, string) error {
		return errTestAuth
	}
	defer func() { configureAuthRemote = prevAuth }()
	Iterate(ctx, "ITER-AUTH-FAIL", IterateCI, "fix")
}

var errTestAuth = &stringErr{"auth failed"}

type stringErr struct{ s string }

func (e *stringErr) Error() string { return e.s }

// TestIterateRebaseConflictAborts covers the "rebase-main" error
// return by setting up a branch whose change conflicts with a later
// push to main, forcing git rebase to abort.
func TestIterateRebaseConflictAborts(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	_ = db.MarkCodeFailed(ctx, "ITER-REBASE", "", "", "", "", "", "", "")
	remote := setupBareRemote(t)
	prev := parseRepoURL
	parseRepoURL = func(string) (string, error) { return "owner/repo", nil }
	defer func() { parseRepoURL = prev }()

	Run(ctx, "ITER-REBASE", "P-1", remote, "impl", "desc")
	got, _ := db.GetCodeTask(ctx, "ITER-REBASE")
	if got == nil || got.Status != data.CodeInReview {
		t.Fatalf("setup: %+v", got)
	}

	// Push a conflicting edit to main from a second clone so rebase
	// will fail.
	other := t.TempDir() + "/other"
	c := osExec("git", "clone", remote, other)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	writeAndCommit(t, other, "implementation.go", "conflict-from-main", "main edit")
	c = osExec("git", "-C", other, "push", "origin", "main")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("push main: %v\n%s", err, out)
	}

	Iterate(ctx, "ITER-REBASE", IterateCI, "fix")
	// Iterate failed mid-way; DB status stays in_review.
	after, _ := db.GetCodeTask(ctx, "ITER-REBASE")
	if after.Status != data.CodeInReview {
		t.Errorf("status = %q", after.Status)
	}
}

func writeAndCommit(t *testing.T, repoDir, name, contents, msg string) {
	t.Helper()
	if err := os.WriteFile(repoDir+"/"+name, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-C", repoDir, "config", "user.email", "t@t"},
		{"-C", repoDir, "config", "user.name", "t"},
		{"-C", repoDir, "add", "."},
		{"-C", repoDir, "commit", "-m", msg},
	} {
		c := osExec("git", args...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestIteratePanicRecovered(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	task := &data.CodeTask{
		IssueKey:      "ITER-PANIC",
		ParentJiraKey: "P-1",
		RepoURL:       "https://x",
		Title:         "x",
		Branch:        "ITER-PANIC",
		Status:        data.CodeInReview,
	}
	if err := db.SaveCodeTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	prev := parseRepoURL
	parseRepoURL = func(string) (string, error) { panic("boom") }
	defer func() { parseRepoURL = prev }()
	Iterate(ctx, "ITER-PANIC", IterateCI, "x")
}
