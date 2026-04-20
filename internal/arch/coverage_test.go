package arch

import (
	"context"
	"errors"
	"testing"

	"github.com/randheer094/velocity/internal/data"
	"github.com/randheer094/velocity/internal/db"
)

// TestAdvanceWavePastEnd hits the archiveDone branch when the plan's
// ActiveWaveIdx is already past the last wave.
func TestAdvanceWavePastEnd(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{
		ParentJiraKey: "ARCH-AW-PAST",
		Name:          "x",
		RepoURL:       "r",
		Waves:         []data.Wave{{Tasks: []data.PlannedTask{{Title: "x", JiraKey: "ARCH-AW-PAST-1"}}}},
		ActiveWaveIdx: 1,
	}
	if err := db.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err := AdvanceWave(ctx, "ARCH-AW-PAST"); err != nil {
		t.Errorf("AdvanceWave: %v", err)
	}
	got, _ := db.GetPlan(ctx, "ARCH-AW-PAST")
	if got.Status != data.PlanDone {
		t.Errorf("status = %q", got.Status)
	}
}

// TestRecordFailureNoExistingPlan exercises the warn branch where
// MarkPlanFailed has no row to update.
func TestRecordFailureNoExistingPlan(t *testing.T) {
	requireDB(t)
	recordFailure(context.Background(), "ARCH-FAIL-NOPLAN", "stage", errors.New("boom"))
}

// TestOnDismissedAlreadyDoneSubtask exercises the cascade switch's
// Done-status skip branch.
func TestOnDismissedAlreadyDoneSubtask(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{
		ParentJiraKey: "ARCH-DC-DONE",
		Name:          "x",
		RepoURL:       "r",
		Waves:         []data.Wave{{Tasks: []data.PlannedTask{{Title: "a", JiraKey: "ARCH-DC-DONE-1"}}}},
	}
	if err := db.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	statusOverride.Store("ARCH-DC-DONE-1", "Dev Failed")
	if err := OnDismissed(ctx, "ARCH-DC-DONE", "Dismissed"); err != nil {
		t.Fatalf("OnDismissed: %v", err)
	}
}

// TestRunBadParentKeyFailsAtProjectKey exercises the projectKey == ""
// branch in plan(). A parent key without a dash yields "". Pre-saved
// failed plan so MarkPlanFailed (UPDATE-only) records the new stage.
func TestRunBadParentKeyFailsAtProjectKey(t *testing.T) {
	requireDB(t)
	remote := setupBareRemote(t)
	presaveFailedPlan(t, "NODASH", remote)
	ctx := context.Background()
	Run(ctx, "NODASH", remote, "title", "do thing")
	got, _ := db.GetPlan(ctx, "NODASH")
	if got == nil || got.LastErrorStage != "create-subtasks" {
		t.Errorf("got = %+v", got)
	}
}

// presaveFailedPlan stamps a failed-plan row so subsequent Run calls hit
// the retry-guard wipe path and so MarkPlanFailed (UPDATE-only) lands.
func presaveFailedPlan(t *testing.T, key, repo string) {
	t.Helper()
	p := &data.Plan{
		ParentJiraKey: key,
		Name:          "x",
		RepoURL:       repo,
		Waves:         []data.Wave{{Tasks: []data.PlannedTask{{Title: "x"}}}},
	}
	if err := db.SavePlan(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkPlanFailed(context.Background(), key, "Planning Failed", "stage", "previous"); err != nil {
		t.Fatal(err)
	}
}

func TestRunLLMFails(t *testing.T) {
	requireDB(t)
	remote := setupBareRemote(t)
	presaveFailedPlan(t, "ARCH-LLM-FAIL", remote)
	t.Setenv("ARCH_TEST_MODE", "fail")
	Run(context.Background(), "ARCH-LLM-FAIL", remote, "t", "r")
	p, _ := db.GetPlan(context.Background(), "ARCH-LLM-FAIL")
	if p == nil || p.Status != data.PlanPlanningFailed {
		t.Errorf("expected planning_failed: %+v", p)
	}
	if p != nil && p.LastErrorStage != "arch-llm" {
		t.Errorf("stage = %q", p.LastErrorStage)
	}
}

func TestRunBadJSONFails(t *testing.T) {
	requireDB(t)
	remote := setupBareRemote(t)
	presaveFailedPlan(t, "ARCH-BAD-JSON", remote)
	t.Setenv("ARCH_TEST_MODE", "bad-json")
	Run(context.Background(), "ARCH-BAD-JSON", remote, "t", "r")
	p, _ := db.GetPlan(context.Background(), "ARCH-BAD-JSON")
	if p == nil || p.LastErrorStage != "parse-plan" {
		t.Errorf("stage = %+v", p)
	}
}

func TestRunEmptyTasksFails(t *testing.T) {
	requireDB(t)
	remote := setupBareRemote(t)
	presaveFailedPlan(t, "ARCH-EMPTY-TASKS", remote)
	t.Setenv("ARCH_TEST_MODE", "empty-tasks")
	Run(context.Background(), "ARCH-EMPTY-TASKS", remote, "t", "r")
	p, _ := db.GetPlan(context.Background(), "ARCH-EMPTY-TASKS")
	if p == nil || p.LastErrorStage != "parse-plan" {
		t.Errorf("stage = %+v", p)
	}
}

func TestRunEmptyWavesFails(t *testing.T) {
	requireDB(t)
	remote := setupBareRemote(t)
	presaveFailedPlan(t, "ARCH-EMPTY-WAVES", remote)
	t.Setenv("ARCH_TEST_MODE", "empty-waves")
	Run(context.Background(), "ARCH-EMPTY-WAVES", remote, "t", "r")
	p, _ := db.GetPlan(context.Background(), "ARCH-EMPTY-WAVES")
	if p == nil || p.LastErrorStage != "parse-plan" {
		t.Errorf("stage = %+v", p)
	}
}

