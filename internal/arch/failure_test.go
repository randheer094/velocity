package arch

import (
	"context"
	"errors"
	"testing"

	"github.com/randheer094/velocity/internal/data"
	"github.com/randheer094/velocity/internal/db"
)

func TestRecordFailureFull(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{
		ParentJiraKey: "ARCH-RF",
		Name:          "x",
		RepoURL:       "r",
		TaskList:      []data.PlannedTask{{ID: "t1", Title: "x"}},
		Waves:         []data.Wave{{Tasks: []data.WaveRef{{ID: "t1"}}}},
	}
	if err := db.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	recordFailure(ctx, "ARCH-RF", "stage1", errors.New("boom"))
	got, _ := db.GetPlan(ctx, "ARCH-RF")
	if got.Status != data.PlanPlanningFailed {
		t.Errorf("status = %q", got.Status)
	}
	if got.LastError != "boom" {
		t.Errorf("err = %q", got.LastError)
	}
}
