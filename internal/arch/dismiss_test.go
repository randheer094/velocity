package arch

import (
	"context"
	"testing"

	"github.com/randheer094/velocity/internal/data"
	"github.com/randheer094/velocity/internal/db"
)

func TestOnDismissedNoPlan(t *testing.T) {
	requireDB(t)
	if err := OnDismissed(context.Background(), "ARCH-NOPE", "Dismissed"); err != nil {
		t.Errorf("OnDismissed missing plan: %v", err)
	}
}

func TestOnDismissedTerminalIgnored(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{
		ParentJiraKey: "ARCH-TD",
		Name:          "x",
		RepoURL:       "r",
		Waves:         []data.Wave{{Tasks: []data.PlannedTask{{Title: "x", JiraKey: "ARCH-TD-1"}}}},
	}
	if err := db.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkPlanDone(ctx, "ARCH-TD", "Done"); err != nil {
		t.Fatal(err)
	}
	if err := OnDismissed(ctx, "ARCH-TD", "Dismissed"); err != nil {
		t.Errorf("OnDismissed terminal: %v", err)
	}
}

func TestOnDismissedCascades(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{
		ParentJiraKey: "ARCH-DC",
		Name:          "x",
		RepoURL:       "r",
		Waves: []data.Wave{
			{Tasks: []data.PlannedTask{{Title: "x", JiraKey: "ARCH-DC-1"}}},
			{Tasks: []data.PlannedTask{{Title: "y", JiraKey: "ARCH-DC-2"}}},
		},
	}
	if err := db.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	// Mark one sub-task as Done; the cascade should skip it.
	statusOverride.Store("ARCH-DC-1", "Done")
	statusOverride.Store("ARCH-DC-2", "In Progress")
	if err := OnDismissed(ctx, "ARCH-DC", "Dismissed"); err != nil {
		t.Fatalf("OnDismissed: %v", err)
	}
	got, _ := db.GetPlan(ctx, "ARCH-DC")
	if got.Status != data.PlanDone || got.JiraStatus != "Dismissed" {
		t.Errorf("plan status = %q jira = %q", got.Status, got.JiraStatus)
	}
}
