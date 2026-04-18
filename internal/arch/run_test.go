package arch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/randheer094/velocity/internal/data"
	"github.com/randheer094/velocity/internal/db"
)

func setupBareRemote(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote.git")
	work := filepath.Join(dir, "work")
	for _, args := range [][]string{
		{"init", "--bare", "--initial-branch=main", remote},
		{"init", "--initial-branch=main", work},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-C", work, "config", "user.email", "t@t"},
		{"-C", work, "config", "user.name", "t"},
		{"-C", work, "add", "."},
		{"-C", work, "commit", "-m", "init"},
		{"-C", work, "remote", "add", "origin", remote},
		{"-C", work, "push", "-u", "origin", "main"},
	} {
		c := exec.Command("git", args...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return remote
}

func TestRunDuplicateClaim(t *testing.T) {
	requireDB(t)
	if !claim("ARCH-RUN-CLAIM") {
		t.Fatal("first claim should succeed")
	}
	defer release("ARCH-RUN-CLAIM")
	// Second invocation hits "already in flight" and returns immediately.
	Run(context.Background(), "ARCH-RUN-CLAIM", "https://x", "t", "r")
}

func TestRunCloneFailureRecordsFailure(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	// Pre-save a plan so MarkPlanFailed (UPDATE-only) takes effect.
	plan := &data.Plan{
		ParentJiraKey: "ARCH-RUN-FAIL",
		Name:          "x",
		RepoURL:       "/nonexistent/repo.git",
		TaskList:      []data.PlannedTask{{ID: "t1", Title: "x"}},
		Waves:         []data.Wave{{Tasks: []data.WaveRef{{ID: "t1"}}}},
	}
	if err := db.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	// Mark it failed first so retry-guard wipes children and re-runs plan() →
	// clone fails → recordFailure called.
	if err := db.MarkPlanFailed(ctx, "ARCH-RUN-FAIL", "x", "y"); err != nil {
		t.Fatal(err)
	}
	Run(ctx, "ARCH-RUN-FAIL", "/nonexistent/repo.git", "t", "do thing")
	got, _ := db.GetPlan(ctx, "ARCH-RUN-FAIL")
	if got == nil || got.Status != data.PlanPlanningFailed {
		t.Errorf("plan = %+v", got)
	}
}

func TestRunRetryGuardTerminalIgnored(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{
		ParentJiraKey: "ARCH-RUN-DONE",
		Name:          "x",
		RepoURL:       "r",
		TaskList:      []data.PlannedTask{{ID: "t1", Title: "x"}},
		Waves:         []data.Wave{{Tasks: []data.WaveRef{{ID: "t1"}}}},
	}
	if err := db.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkPlanDone(ctx, "ARCH-RUN-DONE"); err != nil {
		t.Fatal(err)
	}
	// Plan is terminal; Run should observe and skip without failing.
	Run(ctx, "ARCH-RUN-DONE", "/nonexistent/repo.git", "t", "r")
	got, _ := db.GetPlan(ctx, "ARCH-RUN-DONE")
	if got.Status != data.PlanDone {
		t.Errorf("status changed: %q", got.Status)
	}
}

func TestRunFullPlanSucceeds(t *testing.T) {
	requireDB(t)
	remote := setupBareRemote(t)
	ctx := context.Background()
	Run(ctx, "PROJ-FULL", remote, "title", "do the thing")
	got, _ := db.GetPlan(ctx, "PROJ-FULL")
	if got == nil {
		t.Fatal("plan not saved")
	}
	if got.Status != data.PlanActive {
		t.Errorf("status = %q", got.Status)
	}
	if len(got.TaskList) != 2 || len(got.Waves) != 2 {
		t.Errorf("structure: %+v", got)
	}
	for _, task := range got.TaskList {
		if task.JiraKey == "" {
			t.Errorf("task %q missing JiraKey", task.ID)
		}
	}
}

func TestRunRetryGuardActiveAdvances(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{
		ParentJiraKey: "ARCH-RUN-ACT",
		Name:          "x",
		RepoURL:       "r",
		TaskList:      []data.PlannedTask{{ID: "t1", Title: "x", JiraKey: "ARCH-RUN-ACT-1"}},
		Waves:         []data.Wave{{Tasks: []data.WaveRef{{ID: "t1", JiraKey: "ARCH-RUN-ACT-1"}}}},
	}
	if err := db.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	statusOverride.Store("ARCH-RUN-ACT-1", "Done")
	// Plan is active; Run should call AdvanceWave instead of re-planning.
	Run(ctx, "ARCH-RUN-ACT", "r", "t", "do")
}
