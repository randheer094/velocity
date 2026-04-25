package code

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/randheer094/velocity/internal/config"
)

// captureEnq swaps EnqueueFn so tests can observe follow-up enqueues.
type enqRow struct {
	Kind    string
	Name    string
	Payload any
}

func captureEnq(t *testing.T) *struct {
	mu   sync.Mutex
	rows []enqRow
} {
	t.Helper()
	prev := EnqueueFn
	cap := &struct {
		mu   sync.Mutex
		rows []enqRow
	}{}
	EnqueueFn = func(kind, name string, payload any) {
		cap.mu.Lock()
		defer cap.mu.Unlock()
		cap.rows = append(cap.rows, enqRow{Kind: kind, Name: name, Payload: payload})
	}
	t.Cleanup(func() { EnqueueFn = prev })
	return cap
}

func TestCleanupSkipsIfInFlight(t *testing.T) {
	if !claim("CODE-CL-INFLIGHT") {
		t.Fatal("first claim should succeed")
	}
	defer release("CODE-CL-INFLIGHT")

	ws := config.WorkspacePath("CODE-CL-INFLIGHT")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "marker"), []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(ws)

	if err := Cleanup(context.Background(), "CODE-CL-INFLIGHT"); err != nil {
		t.Errorf("Cleanup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "marker")); err != nil {
		t.Errorf("workspace was wiped despite in-flight claim: %v", err)
	}
}

func TestCleanupRemovesWhenIdle(t *testing.T) {
	ws := config.WorkspacePath("CODE-CL-IDLE")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Cleanup(context.Background(), "CODE-CL-IDLE"); err != nil {
		t.Errorf("Cleanup: %v", err)
	}
	if _, err := os.Stat(ws); !os.IsNotExist(err) {
		t.Errorf("expected workspace removed: %v", err)
	}
}

func TestMarkMergedEnqueuesCleanup(t *testing.T) {
	requireDB(t)
	cleanCodeTask(t, "CODE-MM-ENQ")
	cap := captureEnq(t)

	if err := MarkMerged(context.Background(), "CODE-MM-ENQ", "https://x"); err != nil {
		t.Errorf("MarkMerged: %v", err)
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	found := 0
	for _, r := range cap.rows {
		if r.Kind == kindCleanup {
			found++
		}
	}
	if found != 1 {
		t.Errorf("expected one cleanup enqueue, got rows: %+v", cap.rows)
	}
}

func TestOnDismissedEnqueuesCleanup(t *testing.T) {
	requireDB(t)
	cleanCodeTask(t, "CODE-OD-ENQ")
	cap := captureEnq(t)

	if err := OnDismissed(context.Background(), "CODE-OD-ENQ", "Dismissed"); err != nil {
		t.Errorf("OnDismissed: %v", err)
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	found := 0
	for _, r := range cap.rows {
		if r.Kind == kindCleanup {
			found++
		}
	}
	if found != 1 {
		t.Errorf("expected one cleanup enqueue, got rows: %+v", cap.rows)
	}
}

func TestIsInFlight(t *testing.T) {
	if IsInFlight("CODE-IF") {
		t.Error("clean state should not report in flight")
	}
	if !claim("CODE-IF") {
		t.Fatal("claim should succeed")
	}
	if !IsInFlight("CODE-IF") {
		t.Error("expected in flight")
	}
	release("CODE-IF")
	if IsInFlight("CODE-IF") {
		t.Error("expected not in flight after release")
	}
}
