package db

import (
	"context"
	"os"
	"testing"
	"testing/fstest"

	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/data"
)

// withNoStart executes fn while the package thinks it is not started.
// Used to exercise ErrNotStarted branches in repo functions.
func withNoStart(t *testing.T, fn func()) {
	t.Helper()
	mu.Lock()
	savedOK, savedPool := dataOK, pool
	dataOK, pool = false, nil
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		dataOK, pool = savedOK, savedPool
		mu.Unlock()
	})
	fn()
}

func TestErrNotStartedEverywhere(t *testing.T) {
	ctx := context.Background()
	withNoStart(t, func() {
		if _, err := GetPlan(ctx, "X"); err != ErrNotStarted {
			t.Errorf("GetPlan: %v", err)
		}
		if err := SavePlan(ctx, nil); err != ErrNotStarted {
			// SavePlan may deref plan.CreatedAt before Shared() check — guard
			// against panic by checking err first.
			t.Errorf("SavePlan: %v", err)
		}
		if err := MarkPlanDone(ctx, "X", "Done"); err != ErrNotStarted {
			t.Errorf("MarkPlanDone: %v", err)
		}
		if err := MarkPlanFailed(ctx, "X", "", "", ""); err != ErrNotStarted {
			t.Errorf("MarkPlanFailed: %v", err)
		}
		if err := MarkPlanDismissed(ctx, "X", "Dismissed"); err != ErrNotStarted {
			t.Errorf("MarkPlanDismissed: %v", err)
		}
		if err := WipePlanChildren(ctx, "X"); err != ErrNotStarted {
			t.Errorf("WipePlanChildren: %v", err)
		}
		if _, err := TaskPredecessors(ctx, "X-1"); err != ErrNotStarted {
			t.Errorf("TaskPredecessors: %v", err)
		}
		if _, err := TaskSuccessors(ctx, "X-1"); err != ErrNotStarted {
			t.Errorf("TaskSuccessors: %v", err)
		}
		if _, err := GetCodeTask(ctx, "X-1"); err != ErrNotStarted {
			t.Errorf("GetCodeTask: %v", err)
		}
		if err := SaveCodeTask(ctx, nil); err != ErrNotStarted {
			t.Errorf("SaveCodeTask: %v", err)
		}
		if err := MarkCodeFailed(ctx, "X-1", "P-1", "r", "t", "X-1", "Dev Failed", "s", "b"); err != ErrNotStarted {
			t.Errorf("MarkCodeFailed: %v", err)
		}
		if err := MarkCodeDismissed(ctx, "X-1", "Dismissed"); err != ErrNotStarted {
			t.Errorf("MarkCodeDismissed: %v", err)
		}
	})
}

// TestStopClosesPool exercises Stop's non-trivial close path. Restarts
// afterwards so later tests still see the pool.
func TestStopClosesPool(t *testing.T) {
	requireDB(t)
	if err := Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if Shared() != nil {
		t.Error("Shared should be nil after Stop")
	}
	if err := Start(context.Background()); err != nil {
		t.Fatalf("restart: %v", err)
	}
}

func TestStartMissingEnvFails(t *testing.T) {
	// Save state, unset env, reset dataOK so Start runs its body.
	saved := map[string]string{}
	for _, k := range []string{config.DBHostEnv, config.DBPortEnv, config.DBUserEnv, config.DBPasswordEnv, config.DBNameEnv} {
		saved[k] = os.Getenv(k)
	}
	mu.Lock()
	okSaved := dataOK
	poolSaved := pool
	dataOK = false
	pool = nil
	mu.Unlock()
	t.Cleanup(func() {
		for k, v := range saved {
			_ = os.Setenv(k, v)
		}
		mu.Lock()
		dataOK, pool = okSaved, poolSaved
		mu.Unlock()
	})

	cases := []struct {
		name  string
		unset string
	}{
		{"host", config.DBHostEnv},
		{"user", config.DBUserEnv},
		{"password", config.DBPasswordEnv},
		{"name", config.DBNameEnv},
		{"port", config.DBPortEnv},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range saved {
				_ = os.Setenv(k, v)
			}
			_ = os.Unsetenv(tc.unset)
			if err := Start(context.Background()); err == nil {
				t.Errorf("Start should fail when %s unset", tc.unset)
			}
		})
	}
}

func TestStartBadPortFails(t *testing.T) {
	savedPort := os.Getenv(config.DBPortEnv)
	mu.Lock()
	okSaved := dataOK
	poolSaved := pool
	dataOK = false
	pool = nil
	mu.Unlock()
	t.Cleanup(func() {
		_ = os.Setenv(config.DBPortEnv, savedPort)
		mu.Lock()
		dataOK, pool = okSaved, poolSaved
		mu.Unlock()
	})
	_ = os.Setenv(config.DBPortEnv, "not-a-port")
	if err := Start(context.Background()); err == nil {
		t.Error("expected port parse error")
	}
}

func TestStartBadHostFails(t *testing.T) {
	savedHost := os.Getenv(config.DBHostEnv)
	mu.Lock()
	okSaved := dataOK
	poolSaved := pool
	dataOK = false
	pool = nil
	mu.Unlock()
	t.Cleanup(func() {
		_ = os.Setenv(config.DBHostEnv, savedHost)
		mu.Lock()
		dataOK, pool = okSaved, poolSaved
		mu.Unlock()
	})
	// Point at a port nothing is listening on.
	_ = os.Setenv(config.DBHostEnv, "127.0.0.1")
	_ = os.Setenv(config.DBPortEnv, "1")
	defer func() { _ = os.Setenv(config.DBPortEnv, "55432") }()
	if err := Start(context.Background()); err == nil {
		t.Error("expected connect failure")
	}
}

func TestApplyMigrationDirect(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	p := Shared()
	// Use a version that won't collide with shipped migrations.
	const synthVer = 99001
	// Clean up any prior row in case the test ran before.
	_, _ = p.Exec(ctx, "DELETE FROM schema_migrations WHERE version = $1", synthVer)
	_, _ = p.Exec(ctx, "DROP TABLE IF EXISTS cov_synth")
	t.Cleanup(func() {
		_, _ = p.Exec(ctx, "DELETE FROM schema_migrations WHERE version = $1", synthVer)
		_, _ = p.Exec(ctx, "DROP TABLE IF EXISTS cov_synth")
	})

	m := migration{version: synthVer, name: "99001_synth.sql", sql: "CREATE TABLE cov_synth (id INT)"}
	if err := applyMigration(ctx, p, m); err != nil {
		t.Fatalf("applyMigration: %v", err)
	}
	// Duplicate apply should fail on INSERT to schema_migrations.
	if err := applyMigration(ctx, p, m); err == nil {
		t.Error("duplicate applyMigration should fail")
	}
}

func TestApplyMigrationBadSQL(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	m := migration{version: 99002, name: "99002_bad.sql", sql: "THIS IS NOT SQL"}
	if err := applyMigration(ctx, Shared(), m); err == nil {
		t.Error("expected applyMigration error on bad SQL")
	}
}

func TestLoadMigrationsReturnsAll(t *testing.T) {
	migs, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(migs) == 0 {
		t.Error("expected at least one migration")
	}
	for i := 1; i < len(migs); i++ {
		if migs[i-1].version >= migs[i].version {
			t.Errorf("migrations not sorted: %d >= %d", migs[i-1].version, migs[i].version)
		}
	}
}

func TestLoadMigrationsFromErrors(t *testing.T) {
	t.Run("missing dir", func(t *testing.T) {
		if _, err := loadMigrationsFrom(fstest.MapFS{}, "missing"); err == nil {
			t.Error("expected error for missing dir")
		}
	})
	t.Run("skips non-sql and dirs", func(t *testing.T) {
		fsys := fstest.MapFS{
			"m/README.md":   {Data: []byte("x")},
			"m/0001_ok.sql": {Data: []byte("SELECT 1")},
		}
		got, err := loadMigrationsFrom(fsys, "m")
		if err != nil {
			t.Fatalf("loadMigrationsFrom: %v", err)
		}
		if len(got) != 1 || got[0].version != 1 {
			t.Errorf("migrations: %+v", got)
		}
	})
	t.Run("name without underscore", func(t *testing.T) {
		fsys := fstest.MapFS{"m/0001.sql": {Data: []byte("x")}}
		if _, err := loadMigrationsFrom(fsys, "m"); err == nil {
			t.Error("expected error for malformed name")
		}
	})
	t.Run("bad version prefix", func(t *testing.T) {
		fsys := fstest.MapFS{"m/abc_init.sql": {Data: []byte("x")}}
		if _, err := loadMigrationsFrom(fsys, "m"); err == nil {
			t.Error("expected error for bad prefix")
		}
	})
	t.Run("duplicate version", func(t *testing.T) {
		fsys := fstest.MapFS{
			"m/0001_a.sql": {Data: []byte("x")},
			"m/0001_b.sql": {Data: []byte("y")},
		}
		if _, err := loadMigrationsFrom(fsys, "m"); err == nil {
			t.Error("expected error for duplicate version")
		}
	})
}

func TestMigrateIsIdempotent(t *testing.T) {
	requireDB(t)
	// Running migrate again should no-op every already-applied row.
	if err := migrate(context.Background(), Shared()); err != nil {
		t.Errorf("second migrate: %v", err)
	}
}

// Canceled-context tests exercise error branches in every function that
// issues a pool Query/Exec/Begin. Pgx propagates ctx cancellation so the
// first operation fails, hitting the `return err` path.
func canceledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestPoolOpsCanceledContext(t *testing.T) {
	requireDB(t)
	ctx := canceledCtx()
	if _, err := GetPlan(ctx, "X"); err == nil {
		t.Error("GetPlan: expected error")
	}
	plan := &data.Plan{
		ParentJiraKey: "CANCELED",
		Name:          "x",
		RepoURL:       "r",
		TaskList:      []data.PlannedTask{{ID: "t1", Title: "x"}},
		Waves:         []data.Wave{{Tasks: []data.WaveRef{{ID: "t1"}}}},
	}
	if err := SavePlan(ctx, plan); err == nil {
		t.Error("SavePlan: expected error")
	}
	if err := MarkPlanDone(ctx, "X", "Done"); err == nil {
		t.Error("MarkPlanDone: expected error")
	}
	if err := MarkPlanFailed(ctx, "X", "Planning Failed", "s", "e"); err == nil {
		t.Error("MarkPlanFailed: expected error")
	}
	if err := MarkPlanDismissed(ctx, "X", "Dismissed"); err == nil {
		t.Error("MarkPlanDismissed: expected error")
	}
	if err := WipePlanChildren(ctx, "X"); err == nil {
		t.Error("WipePlanChildren: expected error")
	}
	if _, err := TaskPredecessors(ctx, "X-1"); err == nil {
		t.Error("TaskPredecessors: expected error")
	}
	if _, err := TaskSuccessors(ctx, "X-1"); err == nil {
		t.Error("TaskSuccessors: expected error")
	}
	if _, err := GetCodeTask(ctx, "X-1"); err == nil {
		t.Error("GetCodeTask: expected error")
	}
	task := &data.CodeTask{IssueKey: "X-1", ParentJiraKey: "P-1", RepoURL: "r", Title: "t", Branch: "X-1"}
	if err := SaveCodeTask(ctx, task); err == nil {
		t.Error("SaveCodeTask: expected error")
	}
	if err := MarkCodeFailed(ctx, "X-1", "P-1", "r", "t", "X-1", "Dev Failed", "s", "b"); err == nil {
		t.Error("MarkCodeFailed: expected error")
	}
	if err := MarkCodeDismissed(ctx, "X-1", "Dismissed"); err == nil {
		t.Error("MarkCodeDismissed: expected error")
	}
}

func TestMigrateAndAppliedVersionsCanceled(t *testing.T) {
	requireDB(t)
	ctx := canceledCtx()
	if err := migrate(ctx, Shared()); err == nil {
		t.Error("migrate: expected error")
	}
	if _, err := appliedVersions(ctx, Shared()); err == nil {
		t.Error("appliedVersions: expected error")
	}
}

func TestApplyMigrationCanceled(t *testing.T) {
	requireDB(t)
	m := migration{version: 99003, name: "99003_synth.sql", sql: "SELECT 1"}
	if err := applyMigration(canceledCtx(), Shared(), m); err == nil {
		t.Error("expected error")
	}
}

// TestLoadPlanErrorsPropagate exercises loadPlanTasks/loadPlanWaves when
// the outer context is canceled mid-load. We need the outer QueryRow to
// succeed but subsequent Query to fail. Simulate by saving a plan, then
// calling loadPlanTasks / loadPlanWaves directly with a canceled ctx.
func TestLoadPlanTasksWavesCanceled(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	plan := &data.Plan{
		ParentJiraKey: "LOAD-CANCEL",
		Name:          "x",
		RepoURL:       "r",
		TaskList:      []data.PlannedTask{{ID: "t1", Title: "x"}},
		Waves:         []data.Wave{{Tasks: []data.WaveRef{{ID: "t1"}}}},
	}
	if err := SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPlanTasks(canceledCtx(), Shared(), "LOAD-CANCEL"); err == nil {
		t.Error("loadPlanTasks: expected error")
	}
	if _, err := loadPlanWaves(canceledCtx(), Shared(), "LOAD-CANCEL"); err == nil {
		t.Error("loadPlanWaves: expected error")
	}
}

// TestSavePlanExistingRowBranches exercises SavePlan's multi-wave path:
// two waves so the wave-index loop, plan_waves insert, and plan_task_deps
// insert all execute.
func TestSavePlanTwoWaves(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	p := &data.Plan{
		ParentJiraKey: "SAVE-TWO",
		Name:          "x",
		RepoURL:       "r",
		TaskList: []data.PlannedTask{
			{ID: "a", Title: "A"},
			{ID: "b", Title: "B"},
		},
		Waves: []data.Wave{
			{Tasks: []data.WaveRef{{ID: "a"}}},
			{Tasks: []data.WaveRef{{ID: "b"}}},
		},
	}
	if err := SavePlan(ctx, p); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}
	// Re-save should exercise the update path (ON CONFLICT).
	if err := SavePlan(ctx, p); err != nil {
		t.Fatalf("SavePlan re-save: %v", err)
	}
	got, _ := GetPlan(ctx, "SAVE-TWO")
	if got == nil || len(got.Waves) != 2 {
		t.Errorf("reload: %+v", got)
	}
}
