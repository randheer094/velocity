package db

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/randheer094/velocity/internal/data"
)

// cleanJobs removes any residual queue rows so tests can count them
// deterministically. Runs before and after each test.
func cleanJobs(t *testing.T) {
	t.Helper()
	_, err := Shared().Exec(context.Background(), "DELETE FROM webhook_jobs")
	if err != nil {
		t.Fatalf("DELETE webhook_jobs: %v", err)
	}
}

func TestInsertAndClaimWebhookJob(t *testing.T) {
	requireDB(t)
	cleanJobs(t)
	t.Cleanup(func() { cleanJobs(t) })
	ctx := context.Background()

	payload := map[string]string{"branch": "PROJ-1", "pr_url": "https://x"}
	id, err := InsertWebhookJob(ctx, "code.MarkMerged", "code.MarkMerged:PROJ-1", payload)
	if err != nil {
		t.Fatalf("InsertWebhookJob: %v", err)
	}
	if id == 0 {
		t.Error("expected id > 0")
	}

	job, err := ClaimNextWebhookJob(ctx)
	if err != nil {
		t.Fatalf("ClaimNextWebhookJob: %v", err)
	}
	if job == nil {
		t.Fatal("expected a claimed job")
	}
	if job.Kind != "code.MarkMerged" {
		t.Errorf("kind = %q", job.Kind)
	}
	if job.Status != data.WebhookJobRunning {
		t.Errorf("status = %q, expected running", job.Status)
	}
	if job.Attempts != 1 {
		t.Errorf("attempts = %d", job.Attempts)
	}
	var got map[string]string
	if err := json.Unmarshal(job.Payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got["branch"] != "PROJ-1" {
		t.Errorf("payload = %+v", got)
	}

	// Next claim: nothing pending.
	empty, err := ClaimNextWebhookJob(ctx)
	if err != nil {
		t.Fatalf("ClaimNextWebhookJob empty: %v", err)
	}
	if empty != nil {
		t.Errorf("expected empty queue, got %+v", empty)
	}
}

func TestMarkWebhookJobDone(t *testing.T) {
	requireDB(t)
	cleanJobs(t)
	t.Cleanup(func() { cleanJobs(t) })
	ctx := context.Background()

	id, err := InsertWebhookJob(ctx, "k", "n", map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimNextWebhookJob(ctx); err != nil {
		t.Fatal(err)
	}
	if err := MarkWebhookJobDone(ctx, id); err != nil {
		t.Fatalf("MarkWebhookJobDone: %v", err)
	}

	var status string
	err = Shared().QueryRow(ctx, "SELECT status FROM webhook_jobs WHERE id=$1", id).Scan(&status)
	if err != nil {
		t.Fatal(err)
	}
	if status != "done" {
		t.Errorf("status = %q", status)
	}
}

func TestMarkWebhookJobFailed(t *testing.T) {
	requireDB(t)
	cleanJobs(t)
	t.Cleanup(func() { cleanJobs(t) })
	ctx := context.Background()

	id, err := InsertWebhookJob(ctx, "k", "n", map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimNextWebhookJob(ctx); err != nil {
		t.Fatal(err)
	}
	if err := MarkWebhookJobFailed(ctx, id, "oh no"); err != nil {
		t.Fatalf("MarkWebhookJobFailed: %v", err)
	}

	var status, msg string
	err = Shared().QueryRow(ctx, "SELECT status, last_error FROM webhook_jobs WHERE id=$1", id).Scan(&status, &msg)
	if err != nil {
		t.Fatal(err)
	}
	if status != "failed" || msg != "oh no" {
		t.Errorf("status=%q msg=%q", status, msg)
	}
}

func TestResetRunningWebhookJobs(t *testing.T) {
	requireDB(t)
	cleanJobs(t)
	t.Cleanup(func() { cleanJobs(t) })
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := InsertWebhookJob(ctx, "k", "n", map[string]int{"i": i}); err != nil {
			t.Fatal(err)
		}
	}
	// Claim 2 rows so they flip to running.
	for i := 0; i < 2; i++ {
		if _, err := ClaimNextWebhookJob(ctx); err != nil {
			t.Fatal(err)
		}
	}

	n, err := ResetRunningWebhookJobs(ctx)
	if err != nil {
		t.Fatalf("ResetRunningWebhookJobs: %v", err)
	}
	if n != 2 {
		t.Errorf("reset count = %d, want 2", n)
	}

	var pending int
	err = Shared().QueryRow(ctx, "SELECT COUNT(*) FROM webhook_jobs WHERE status='pending'").Scan(&pending)
	if err != nil {
		t.Fatal(err)
	}
	if pending != 3 {
		t.Errorf("pending after reset = %d, want 3", pending)
	}
}

func TestCountPendingWebhookJobs(t *testing.T) {
	requireDB(t)
	cleanJobs(t)
	t.Cleanup(func() { cleanJobs(t) })
	ctx := context.Background()

	for i := 0; i < 4; i++ {
		if _, err := InsertWebhookJob(ctx, "k", "n", map[string]int{"i": i}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := CountPendingWebhookJobs(ctx)
	if err != nil {
		t.Fatalf("CountPendingWebhookJobs: %v", err)
	}
	if n != 4 {
		t.Errorf("pending = %d, want 4", n)
	}
}

// TestClaimNextWebhookJobFIFO confirms oldest-first ordering.
func TestClaimNextWebhookJobFIFO(t *testing.T) {
	requireDB(t)
	cleanJobs(t)
	t.Cleanup(func() { cleanJobs(t) })
	ctx := context.Background()

	id1, _ := InsertWebhookJob(ctx, "k", "first", map[string]string{})
	id2, _ := InsertWebhookJob(ctx, "k", "second", map[string]string{})
	id3, _ := InsertWebhookJob(ctx, "k", "third", map[string]string{})

	for _, want := range []int64{id1, id2, id3} {
		j, err := ClaimNextWebhookJob(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if j == nil || j.ID != want {
			t.Errorf("got %+v, want id %d", j, want)
		}
	}
}

// TestClaimNextWebhookJobParallel exercises FOR UPDATE SKIP LOCKED —
// two parallel claims must hand out distinct rows.
func TestClaimNextWebhookJobParallel(t *testing.T) {
	requireDB(t)
	cleanJobs(t)
	t.Cleanup(func() { cleanJobs(t) })
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		if _, err := InsertWebhookJob(ctx, "k", "n", map[string]int{"i": i}); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	seen := make(map[int64]bool)
	var seenMu sync.Mutex
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j, err := ClaimNextWebhookJob(ctx)
			if err != nil || j == nil {
				return
			}
			seenMu.Lock()
			if seen[j.ID] {
				t.Errorf("id %d claimed twice", j.ID)
			}
			seen[j.ID] = true
			seenMu.Unlock()
		}()
	}
	wg.Wait()
	if len(seen) != 10 {
		t.Errorf("expected 10 distinct claims, got %d", len(seen))
	}
}

func TestWebhookJobsNotStarted(t *testing.T) {
	withNoStart(t, func() {
		ctx := context.Background()
		if _, err := InsertWebhookJob(ctx, "k", "n", map[string]string{}); err != ErrNotStarted {
			t.Errorf("InsertWebhookJob: %v", err)
		}
		if _, err := ClaimNextWebhookJob(ctx); err != ErrNotStarted {
			t.Errorf("ClaimNextWebhookJob: %v", err)
		}
		if err := MarkWebhookJobDone(ctx, 1); err != ErrNotStarted {
			t.Errorf("MarkWebhookJobDone: %v", err)
		}
		if err := MarkWebhookJobFailed(ctx, 1, "x"); err != ErrNotStarted {
			t.Errorf("MarkWebhookJobFailed: %v", err)
		}
		if _, err := ResetRunningWebhookJobs(ctx); err != ErrNotStarted {
			t.Errorf("ResetRunningWebhookJobs: %v", err)
		}
		if _, err := CountPendingWebhookJobs(ctx); err != ErrNotStarted {
			t.Errorf("CountPendingWebhookJobs: %v", err)
		}
	})
}

// TestInsertWebhookJobMarshalError exercises the json.Marshal error
// path (channels can't be marshalled).
func TestInsertWebhookJobMarshalError(t *testing.T) {
	requireDB(t)
	_, err := InsertWebhookJob(context.Background(), "k", "n", make(chan int))
	if err == nil {
		t.Error("expected marshal error")
	}
}

func TestWebhookJobsCanceledCtx(t *testing.T) {
	requireDB(t)
	ctx := canceledCtx()
	if _, err := InsertWebhookJob(ctx, "k", "n", map[string]string{}); err == nil {
		t.Error("InsertWebhookJob: expected error")
	}
	if _, err := ClaimNextWebhookJob(ctx); err == nil {
		t.Error("ClaimNextWebhookJob: expected error")
	}
	if err := MarkWebhookJobDone(ctx, 1); err == nil {
		t.Error("MarkWebhookJobDone: expected error")
	}
	if err := MarkWebhookJobFailed(ctx, 1, "x"); err == nil {
		t.Error("MarkWebhookJobFailed: expected error")
	}
	if _, err := ResetRunningWebhookJobs(ctx); err == nil {
		t.Error("ResetRunningWebhookJobs: expected error")
	}
	if _, err := CountPendingWebhookJobs(ctx); err == nil {
		t.Error("CountPendingWebhookJobs: expected error")
	}
}
