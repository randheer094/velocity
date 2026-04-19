package db

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/data"
)

func testDBConfig() config.DatabaseConfig {
	c := config.DatabaseConfig{}
	// applyDefaults lives on *Config; fill defaults by hand here.
	c.Port = 5432
	c.User = "velocity"
	c.Name = "velocity"
	c.SSLMode = "disable"
	return c
}

var dbReady bool

func TestMain(m *testing.M) {
	if os.Getenv(config.DBHostEnv) != "" && os.Getenv(config.DBPasswordEnv) != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := Start(ctx, testDBConfig()); err == nil {
			dbReady = true
		} else {
			os.Stderr.WriteString("db-backed tests skipped: " + err.Error() + "\n")
		}
	} else {
		os.Stderr.WriteString("db-backed tests skipped: " + config.DBHostEnv + " / " + config.DBPasswordEnv + " not set\n")
	}
	code := m.Run()
	if dbReady {
		_ = Stop()
	}
	os.Exit(code)
}

func requireDB(t *testing.T) {
	t.Helper()
	if !dbReady {
		t.Skipf("requires a running Postgres; set %s and %s", config.DBHostEnv, config.DBPasswordEnv)
	}
}

func TestStartIdempotent(t *testing.T) {
	requireDB(t)
	if err := Start(context.Background(), testDBConfig()); err != nil {
		t.Errorf("second Start: %v", err)
	}
}

func TestStopBeforeStartIsNoop(t *testing.T) {
	// Save state, mimic not-started, restore.
	mu.Lock()
	saved := dataOK
	dataOK = false
	mu.Unlock()
	if err := Stop(); err != nil {
		t.Errorf("Stop on idle: %v", err)
	}
	mu.Lock()
	dataOK = saved
	mu.Unlock()
}

func TestSharedReturns(t *testing.T) {
	requireDB(t)
	if Shared() == nil {
		t.Error("Shared should be non-nil after Start")
	}
}

func TestSavePlanAndGetPlan(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{
		ParentJiraKey: "PROJ-1",
		Name:          "test plan",
		RepoURL:       "https://github.com/o/r.git",
		TaskList: []data.PlannedTask{
			{ID: "t1", Title: "first", Description: "do first", JiraKey: "PROJ-2"},
			{ID: "t2", Title: "second", JiraKey: "PROJ-3"},
		},
		Waves: []data.Wave{
			{Tasks: []data.WaveRef{{ID: "t1", JiraKey: "PROJ-2"}}},
			{Tasks: []data.WaveRef{{ID: "t2", JiraKey: "PROJ-3"}}},
		},
	}
	if err := SavePlan(ctx, plan); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}
	got, err := GetPlan(ctx, "PROJ-1")
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if got == nil || got.Name != "test plan" {
		t.Errorf("got = %+v", got)
	}
	if len(got.TaskList) != 2 || len(got.Waves) != 2 {
		t.Errorf("plan structure off: %+v", got)
	}

	// Predecessors / successors via deps
	preds, err := TaskPredecessors(ctx, "PROJ-3")
	if err != nil || len(preds) != 1 || preds[0] != "PROJ-2" {
		t.Errorf("preds = %v err=%v", preds, err)
	}
	succs, err := TaskSuccessors(ctx, "PROJ-2")
	if err != nil || len(succs) != 1 || succs[0] != "PROJ-3" {
		t.Errorf("succs = %v err=%v", succs, err)
	}
}

func TestGetPlanMissing(t *testing.T) {
	requireDB(t)
	got, err := GetPlan(context.Background(), "NOPE-1")
	if err != nil {
		t.Errorf("err = %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestMarkPlanDone(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{
		ParentJiraKey: "PROJ-D",
		Name:          "done",
		RepoURL:       "r",
		TaskList:      []data.PlannedTask{{ID: "t1", Title: "x"}},
		Waves:         []data.Wave{{Tasks: []data.WaveRef{{ID: "t1"}}}},
	}
	if err := SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err := MarkPlanDone(ctx, "PROJ-D"); err != nil {
		t.Fatal(err)
	}
	got, _ := GetPlan(ctx, "PROJ-D")
	if got.Status != data.PlanDone {
		t.Errorf("status = %q", got.Status)
	}
}

func TestMarkPlanFailedAndDismissed(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{
		ParentJiraKey: "PROJ-F",
		Name:          "f",
		RepoURL:       "r",
		TaskList:      []data.PlannedTask{{ID: "t1", Title: "x"}},
		Waves:         []data.Wave{{Tasks: []data.WaveRef{{ID: "t1"}}}},
	}
	if err := SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err := MarkPlanFailed(ctx, "PROJ-F", "stage", "boom"); err != nil {
		t.Fatal(err)
	}
	got, _ := GetPlan(ctx, "PROJ-F")
	if got.Status != data.PlanPlanningFailed || got.LastError != "boom" || got.LastErrorStage != "stage" {
		t.Errorf("got = %+v", got)
	}
	if err := MarkPlanDismissed(ctx, "PROJ-F"); err != nil {
		t.Fatal(err)
	}
	got, _ = GetPlan(ctx, "PROJ-F")
	if got.Status != data.PlanDismissed {
		t.Errorf("status = %q", got.Status)
	}
}

func TestWipePlanChildren(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{
		ParentJiraKey: "PROJ-W",
		Name:          "w",
		RepoURL:       "r",
		TaskList:      []data.PlannedTask{{ID: "t1", Title: "x"}, {ID: "t2", Title: "y"}},
		Waves: []data.Wave{
			{Tasks: []data.WaveRef{{ID: "t1"}}},
			{Tasks: []data.WaveRef{{ID: "t2"}}},
		},
	}
	if err := SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err := WipePlanChildren(ctx, "PROJ-W"); err != nil {
		t.Fatal(err)
	}
	got, _ := GetPlan(ctx, "PROJ-W")
	if len(got.TaskList) != 0 {
		t.Errorf("tasks not wiped: %+v", got.TaskList)
	}
	if len(got.Waves) != 0 {
		t.Errorf("waves not wiped: %+v", got.Waves)
	}
}

func TestSaveCodeTaskAndGet(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	task := &data.CodeTask{
		IssueKey:      "PROJ-2",
		ParentJiraKey: "PROJ-1",
		RepoURL:       "https://github.com/o/r.git",
		Title:         "do",
		Description:   "do thing",
		Branch:        "PROJ-2",
		Status:        data.CodeInProgress,
	}
	if err := SaveCodeTask(ctx, task); err != nil {
		t.Fatalf("SaveCodeTask: %v", err)
	}
	got, err := GetCodeTask(ctx, "PROJ-2")
	if err != nil {
		t.Fatalf("GetCodeTask: %v", err)
	}
	if got == nil || got.Title != "do" || got.Status != data.CodeInProgress {
		t.Errorf("got = %+v", got)
	}
}

func TestGetCodeTaskMissing(t *testing.T) {
	requireDB(t)
	got, err := GetCodeTask(context.Background(), "NOPE-X")
	if err != nil {
		t.Errorf("err = %v", err)
	}
	if got != nil {
		t.Errorf("expected nil: %+v", got)
	}
}

func TestMarkCodeFailed(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	if err := MarkCodeFailed(ctx, "PROJ-NEW", "PROJ-1", "https://r", "title", "PROJ-NEW", "stage", "err"); err != nil {
		t.Fatal(err)
	}
	got, _ := GetCodeTask(ctx, "PROJ-NEW")
	if got == nil || got.Status != data.CodeFailed || got.Error != "err" || got.LastErrorStage != "stage" {
		t.Errorf("got = %+v", got)
	}
}

func TestMarkCodeFailedExisting(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	task := &data.CodeTask{IssueKey: "PROJ-EX", ParentJiraKey: "PROJ-1", RepoURL: "r", Title: "x", Branch: "PROJ-EX", Status: data.CodeInProgress}
	if err := SaveCodeTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := MarkCodeFailed(ctx, "PROJ-EX", "PROJ-1", "r", "x", "PROJ-EX", "stage", "boom"); err != nil {
		t.Fatal(err)
	}
	got, _ := GetCodeTask(ctx, "PROJ-EX")
	if got.Status != data.CodeFailed {
		t.Errorf("status = %q", got.Status)
	}
}

func TestMarkCodeDismissed(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	task := &data.CodeTask{IssueKey: "PROJ-DM", ParentJiraKey: "PROJ-1", RepoURL: "r", Title: "x", Branch: "PROJ-DM", Status: data.CodeInProgress}
	if err := SaveCodeTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := MarkCodeDismissed(ctx, "PROJ-DM"); err != nil {
		t.Fatal(err)
	}
	got, _ := GetCodeTask(ctx, "PROJ-DM")
	if got.Status != data.CodeDismissed {
		t.Errorf("status = %q", got.Status)
	}
	// Dismissing a missing task is a no-op
	if err := MarkCodeDismissed(ctx, "MISSING-1"); err != nil {
		t.Errorf("missing dismiss: %v", err)
	}
}

func TestSavePlanPreservesCreatedAtAndStatus(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	earlier := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	plan := &data.Plan{
		ParentJiraKey: "PROJ-CR",
		Name:          "preset",
		RepoURL:       "r",
		TaskList:      []data.PlannedTask{{ID: "t1", Title: "x"}},
		Waves:         []data.Wave{{Tasks: []data.WaveRef{{ID: "t1"}}}},
		CreatedAt:     earlier,
		Status:        data.PlanDone,
	}
	if err := SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	got, _ := GetPlan(ctx, "PROJ-CR")
	if got == nil || got.Status != data.PlanDone {
		t.Errorf("got = %+v", got)
	}
	if got != nil && !got.CreatedAt.Equal(earlier) {
		t.Errorf("CreatedAt overwritten: got %v want %v", got.CreatedAt, earlier)
	}
}

func TestSavePlanIdempotent(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{
		ParentJiraKey: "PROJ-I",
		Name:          "first",
		RepoURL:       "r",
		TaskList:      []data.PlannedTask{{ID: "t1", Title: "x"}},
		Waves:         []data.Wave{{Tasks: []data.WaveRef{{ID: "t1"}}}},
	}
	if err := SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	plan.Name = "second"
	if err := SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	got, _ := GetPlan(ctx, "PROJ-I")
	if got.Name != "second" {
		t.Errorf("name = %q", got.Name)
	}
}

// TestNotStartedReturnsErr cycles dataOK=false to exercise the
// `Shared() == nil → return ErrNotStarted` branches in every repo
// helper. Restores state in t.Cleanup.
func TestNotStartedReturnsErr(t *testing.T) {
	mu.Lock()
	savedPool := pool
	pool = nil
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		pool = savedPool
		mu.Unlock()
	})

	ctx := context.Background()
	if err := SavePlan(ctx, &data.Plan{ParentJiraKey: "X"}); err != ErrNotStarted {
		t.Errorf("SavePlan: %v", err)
	}
	if _, err := GetPlan(ctx, "X"); err != ErrNotStarted {
		t.Errorf("GetPlan: %v", err)
	}
	if err := MarkPlanDone(ctx, "X"); err != ErrNotStarted {
		t.Errorf("MarkPlanDone: %v", err)
	}
	if err := MarkPlanFailed(ctx, "X", "s", "e"); err != ErrNotStarted {
		t.Errorf("MarkPlanFailed: %v", err)
	}
	if err := MarkPlanDismissed(ctx, "X"); err != ErrNotStarted {
		t.Errorf("MarkPlanDismissed: %v", err)
	}
	if err := WipePlanChildren(ctx, "X"); err != ErrNotStarted {
		t.Errorf("WipePlanChildren: %v", err)
	}
	if _, err := TaskPredecessors(ctx, "X"); err != ErrNotStarted {
		t.Errorf("TaskPredecessors: %v", err)
	}
	if _, err := TaskSuccessors(ctx, "X"); err != ErrNotStarted {
		t.Errorf("TaskSuccessors: %v", err)
	}
	if err := SaveCodeTask(ctx, &data.CodeTask{IssueKey: "X"}); err != ErrNotStarted {
		t.Errorf("SaveCodeTask: %v", err)
	}
	if _, err := GetCodeTask(ctx, "X"); err != ErrNotStarted {
		t.Errorf("GetCodeTask: %v", err)
	}
	if err := MarkCodeFailed(ctx, "X", "Y", "r", "t", "X", "s", "e"); err != ErrNotStarted {
		t.Errorf("MarkCodeFailed: %v", err)
	}
	if err := MarkCodeDismissed(ctx, "X"); err != ErrNotStarted {
		t.Errorf("MarkCodeDismissed: %v", err)
	}
}
