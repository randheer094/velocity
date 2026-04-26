package arch

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/data"
	"github.com/randheer094/velocity/internal/db"
)

// TestPlanRetryGuardEnqueuesAdvance: a Run on a parent whose plan is
// already in PlanCoding must enqueue arch.AdvanceWave and return,
// instead of inline-calling AdvanceWave. One step per event.
func TestPlanRetryGuardEnqueuesAdvance(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{
		ParentJiraKey: "ARCH-RG-ENQ",
		Name:          "x",
		RepoURL:       "r",
		Waves:         []data.Wave{{Tasks: []data.PlannedTask{{Title: "x", JiraKey: "ARCH-RG-ENQ-1"}}}},
		Status:        data.PlanCoding,
	}
	if err := db.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	cap := captureEnqueue(t)
	Run(ctx, "ARCH-RG-ENQ", "r", "t", "do")
	if cap.count(kindAdvanceWave) != 1 {
		t.Errorf("expected one advance enqueue, got %v", cap.kinds())
	}
}

// TestAdvanceWaveAdvancesEnqueuesAssign: when AdvanceWave bumps the
// active wave it must enqueue AssignWave for the new index, not
// inline-call assignWave.
func TestAdvanceWaveAdvancesEnqueuesAssign(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{
		ParentJiraKey: "ARCH-AW-ENQ",
		Name:          "x",
		RepoURL:       "r",
		Waves: []data.Wave{
			{Tasks: []data.PlannedTask{{Title: "a", JiraKey: "ARCH-AW-ENQ-1"}}},
			{Tasks: []data.PlannedTask{{Title: "b", JiraKey: "ARCH-AW-ENQ-2"}}},
		},
	}
	if err := db.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	statusOverride.Store("ARCH-AW-ENQ-1", "Done")
	cap := captureEnqueue(t)
	if err := AdvanceWave(ctx, "ARCH-AW-ENQ"); err != nil {
		t.Errorf("AdvanceWave: %v", err)
	}
	if cap.count(kindAssignWave) != 1 {
		t.Errorf("expected one assign-wave enqueue, got %v", cap.kinds())
	}
	got, _ := db.GetPlan(ctx, "ARCH-AW-ENQ")
	if got.ActiveWaveIdx != 1 {
		t.Errorf("active wave idx = %d", got.ActiveWaveIdx)
	}
}

// TestAdvanceWaveEmptyEnqueuesReadvance: an empty wave (no JiraKeys)
// must save the plan and enqueue another AdvanceWave instead of
// recursing inline.
func TestAdvanceWaveEmptyEnqueuesReadvance(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{
		ParentJiraKey: "ARCH-AW-EMPTY-ENQ",
		Name:          "x",
		RepoURL:       "r",
		Waves: []data.Wave{
			{Tasks: []data.PlannedTask{{Title: "blank"}}}, // no JiraKey
			{Tasks: []data.PlannedTask{{Title: "real", JiraKey: "ARCH-AW-EMPTY-ENQ-1"}}},
		},
	}
	if err := db.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	cap := captureEnqueue(t)
	if err := AdvanceWave(ctx, "ARCH-AW-EMPTY-ENQ"); err != nil {
		t.Errorf("AdvanceWave: %v", err)
	}
	if cap.count(kindAdvanceWave) != 1 {
		t.Errorf("expected re-advance enqueue, got %v", cap.kinds())
	}
	got, _ := db.GetPlan(ctx, "ARCH-AW-EMPTY-ENQ")
	if got.ActiveWaveIdx != 1 {
		t.Errorf("active wave idx = %d", got.ActiveWaveIdx)
	}
}

// TestArchiveSkipsWorkspaceWhenInFlight: Archive marks the plan done
// even when the parent's Run is still claimed, but skips the
// workspace cleanup so it doesn't yank files from a live Run.
func TestArchiveSkipsWorkspaceWhenInFlight(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{
		ParentJiraKey: "ARCH-ARCH-INFLIGHT",
		Name:          "x",
		RepoURL:       "r",
		Waves:         []data.Wave{{Tasks: []data.PlannedTask{{Title: "x", JiraKey: "ARCH-ARCH-INFLIGHT-1"}}}},
	}
	if err := db.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	// Materialise a workspace dir so we can verify it stays.
	ws := config.WorkspacePath("ARCH-ARCH-INFLIGHT")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "marker"), []byte("present"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !claim("ARCH-ARCH-INFLIGHT") {
		t.Fatal("first claim should succeed")
	}
	defer release("ARCH-ARCH-INFLIGHT")

	if err := Archive(ctx, "ARCH-ARCH-INFLIGHT"); err != nil {
		t.Errorf("Archive: %v", err)
	}
	got, _ := db.GetPlan(ctx, "ARCH-ARCH-INFLIGHT")
	if got.Status != data.PlanDone {
		t.Errorf("plan status = %q", got.Status)
	}
	if _, err := os.Stat(filepath.Join(ws, "marker")); err != nil {
		t.Errorf("workspace was wiped despite in-flight Run: %v", err)
	}
}

// TestArchiveRemovesWorkspaceWhenIdle: Archive does the FS cleanup
// once the LLM-side claim is released.
func TestArchiveRemovesWorkspaceWhenIdle(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{
		ParentJiraKey: "ARCH-ARCH-IDLE",
		Name:          "x",
		RepoURL:       "r",
		Waves:         []data.Wave{{Tasks: []data.PlannedTask{{Title: "x", JiraKey: "ARCH-ARCH-IDLE-1"}}}},
	}
	if err := db.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	ws := config.WorkspacePath("ARCH-ARCH-IDLE")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Archive(ctx, "ARCH-ARCH-IDLE"); err != nil {
		t.Errorf("Archive: %v", err)
	}
	if _, err := os.Stat(ws); !os.IsNotExist(err) {
		t.Errorf("workspace should have been removed: %v", err)
	}
}

// TestArchiveNoPlan: Archive on an unknown parent is a no-op.
func TestArchiveNoPlan(t *testing.T) {
	requireDB(t)
	if err := Archive(context.Background(), "ARCH-ARCH-NONE"); err != nil {
		t.Errorf("Archive: %v", err)
	}
}

// TestIsInFlight reflects claim/release.
func TestIsInFlight(t *testing.T) {
	if IsInFlight("ARCH-IF") {
		t.Error("clean state should not report in flight")
	}
	if !claim("ARCH-IF") {
		t.Fatal("first claim should succeed")
	}
	if !IsInFlight("ARCH-IF") {
		t.Error("expected in flight after claim")
	}
	release("ARCH-IF")
	if IsInFlight("ARCH-IF") {
		t.Error("expected not in flight after release")
	}
}
