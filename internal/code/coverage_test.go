package code

import (
	"context"
	"errors"
	"testing"

	"github.com/randheer094/velocity/internal/data"
	"github.com/randheer094/velocity/internal/db"
)

// TestRunPanicRecovered exercises the deferred recover() in Run.
func TestRunPanicRecovered(t *testing.T) {
	requireDB(t)
	prev := parseRepoURL
	parseRepoURL = func(string) (string, error) { panic("synthetic") }
	defer func() { parseRepoURL = prev }()

	Run(context.Background(), "CODE-PANIC", "P-1", "https://x", "t", "d")
	got, _ := db.GetCodeTask(context.Background(), "CODE-PANIC")
	if got == nil || got.Status != data.CodeCodingFailed {
		t.Errorf("expected failed: %+v", got)
	}
	if got != nil && got.LastErrorStage != "panic" {
		t.Errorf("stage = %q, want panic", got.LastErrorStage)
	}
}

// TestRunPRURLEmptyFails covers the "failed to open PR" branch.
func TestRunPRURLEmptyFails(t *testing.T) {
	requireDB(t)
	remote := setupBareRemote(t)

	prev := parseRepoURL
	parseRepoURL = func(string) (string, error) { return "fail/pr", nil }
	defer func() { parseRepoURL = prev }()

	Run(context.Background(), "CODE-PR-EMPTY", "P-1", remote, "implement", "desc")
	got, _ := db.GetCodeTask(context.Background(), "CODE-PR-EMPTY")
	if got == nil || got.LastErrorStage != "open-pr" {
		t.Errorf("got = %+v", got)
	}
}

// TestMarkMergedTransitionFails covers the Transition-failed branch in
// MarkMerged. The fakeJira returns an empty transition list for keys
// prefixed FAIL-TRANS-, forcing client.Transition to return false.
func TestMarkMergedTransitionFails(t *testing.T) {
	requireDB(t)
	err := MarkMerged(context.Background(), "FAIL-TRANS-MM", "")
	if err == nil {
		t.Error("expected error from MarkMerged when Transition fails")
	}
}

// TestRecordFailureMarkErrLogs covers the slog.Warn branch when
// db.MarkCodeFailed itself errors out (canceled ctx).
func TestRecordFailureMarkErrLogs(t *testing.T) {
	requireDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	recordFailure(ctx, "CODE-RF-CANCEL", "P-1", "r", "t", "stage", errors.New("boom"))
}

// TestOnDismissedMarkErrLogs covers the slog.Warn branch when
// db.MarkCodeDismissed errors (canceled ctx).
func TestOnDismissedMarkErrLogs(t *testing.T) {
	requireDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := OnDismissed(ctx, "CODE-DM-CANCEL", "Dismissed"); err != nil {
		t.Errorf("OnDismissed: %v", err)
	}
}
