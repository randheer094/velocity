package arch

import (
	"context"
	"testing"

	"github.com/randheer094/velocity/internal/data"
	"github.com/randheer094/velocity/internal/db"
)

func TestAdvanceWaveNoPlan(t *testing.T) {
	requireDB(t)
	if err := AdvanceWave(context.Background(), "ARCH-NO-PLAN"); err != nil {
		t.Errorf("AdvanceWave: %v", err)
	}
}

func TestAdvanceWaveStillInProgress(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{
		ParentJiraKey: "ARCH-AW-1",
		Name:          "x",
		RepoURL:       "r",
		Waves:         []data.Wave{{Tasks: []data.PlannedTask{{Title: "x", JiraKey: "ARCH-AW-1-1"}}}},
	}
	if err := db.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	statusOverride.Store("ARCH-AW-1-1", "In Progress")
	if err := AdvanceWave(ctx, "ARCH-AW-1"); err != nil {
		t.Errorf("AdvanceWave: %v", err)
	}
}

func TestAdvanceWaveAdvances(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{
		ParentJiraKey: "ARCH-AW-2",
		Name:          "x",
		RepoURL:       "r",
		Waves: []data.Wave{
			{Tasks: []data.PlannedTask{{Title: "x", JiraKey: "ARCH-AW-2-1"}}},
			{Tasks: []data.PlannedTask{{Title: "y", JiraKey: "ARCH-AW-2-2"}}},
		},
	}
	if err := db.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	statusOverride.Store("ARCH-AW-2-1", "Done")
	if err := AdvanceWave(ctx, "ARCH-AW-2"); err != nil {
		t.Errorf("AdvanceWave: %v", err)
	}
	got, _ := db.GetPlan(ctx, "ARCH-AW-2")
	if got.ActiveWaveIdx != 1 {
		t.Errorf("active wave idx = %d", got.ActiveWaveIdx)
	}
}

func TestAdvanceWaveCompletes(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{
		ParentJiraKey: "ARCH-AW-3",
		Name:          "x",
		RepoURL:       "r",
		Waves:         []data.Wave{{Tasks: []data.PlannedTask{{Title: "x", JiraKey: "ARCH-AW-3-1"}}}},
	}
	if err := db.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	statusOverride.Store("ARCH-AW-3-1", "Done")

	cap := captureEnqueue(t)
	if err := AdvanceWave(ctx, "ARCH-AW-3"); err != nil {
		t.Errorf("AdvanceWave: %v", err)
	}
	if !cap.has(kindArchive) {
		t.Errorf("expected %s enqueued, got %v", kindArchive, cap.kinds())
	}
	// AdvanceWave defers archival to the Archive handler — plan stays
	// PlanCoding until Archive runs.
	if err := Archive(ctx, "ARCH-AW-3"); err != nil {
		t.Errorf("Archive: %v", err)
	}
	got, _ := db.GetPlan(ctx, "ARCH-AW-3")
	if got.Status != data.PlanDone {
		t.Errorf("plan status = %q", got.Status)
	}
}

func TestAssignWaveOutOfRange(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{ParentJiraKey: "ARCH-AS-1", Name: "x", RepoURL: "r", Waves: []data.Wave{}}
	if err := db.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err := AssignWave(ctx, "ARCH-AS-1", 0); err == nil {
		t.Error("expected out-of-range error")
	}
	if err := AssignWave(ctx, "ARCH-AS-1", -1); err == nil {
		t.Error("expected out-of-range error for -1")
	}
}

func TestAssignWaveSkipsBlankKey(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{
		ParentJiraKey: "ARCH-AS-2",
		Name:          "x",
		RepoURL:       "r",
		Waves:         []data.Wave{{Tasks: []data.PlannedTask{{Title: "t-x"}}}}, // no JiraKey
	}
	if err := db.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err := AssignWave(ctx, "ARCH-AS-2", 0); err != nil {
		t.Errorf("AssignWave: %v", err)
	}
}

func TestAssignWaveNoPlan(t *testing.T) {
	requireDB(t)
	if err := AssignWave(context.Background(), "ARCH-AS-NONE", 0); err != nil {
		t.Errorf("AssignWave with no plan: %v", err)
	}
}

func TestAdvanceWaveSkipsEmpty(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{
		ParentJiraKey: "ARCH-AW-4",
		Name:          "x",
		RepoURL:       "r",
		Waves: []data.Wave{
			{Tasks: []data.PlannedTask{{Title: "t-skip"}}}, // no JiraKey
			{Tasks: []data.PlannedTask{{Title: "x", JiraKey: "ARCH-AW-4-1"}}},
		},
	}
	if err := db.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	statusOverride.Store("ARCH-AW-4-1", "Done")
	if err := AdvanceWave(ctx, "ARCH-AW-4"); err != nil {
		t.Errorf("AdvanceWave: %v", err)
	}
}
