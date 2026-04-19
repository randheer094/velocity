package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/randheer094/velocity/internal/data"
)

// resetQueue tears down global state so subsequent tests start clean.
// Shared by every test in this package; kept unexported.
func resetQueue() {
	queueMu.Lock()
	cancel := pollCancel
	queueStart = false
	pollCtx = nil
	pollCancel = nil
	queueSoftCap = 0
	queueMu.Unlock()
	if cancel != nil {
		cancel()
	}
	workerWg.Wait()
}

// restoreVars snapshots every package-level DB seam so tests can
// stub freely and unwind on Cleanup. Call from each test that
// replaces any of these vars.
func restoreVars(t *testing.T) {
	t.Helper()
	savedInsert := insertJob
	savedClaim := claimJob
	savedDone := markDone
	savedFail := markFail
	savedReset := resetRun
	savedCount := countPend
	savedDispatch := dispatch
	t.Cleanup(func() {
		insertJob = savedInsert
		claimJob = savedClaim
		markDone = savedDone
		markFail = savedFail
		resetRun = savedReset
		countPend = savedCount
		dispatch = savedDispatch
	})
}

// fakeInsert records what Enqueue wrote. Callers read Names()/Kinds().
type fakeInsert struct {
	mu    sync.Mutex
	kinds []string
	names []string
	// payloads can be inspected by the rare test that cares.
	payloads []json.RawMessage
	nextID   int64
	err      error
}

func (f *fakeInsert) Fn(_ context.Context, kind, name string, payload any) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kinds = append(f.kinds, kind)
	f.names = append(f.names, name)
	f.payloads = append(f.payloads, raw)
	f.nextID++
	return f.nextID, nil
}

func (f *fakeInsert) Names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.names))
	copy(out, f.names)
	return out
}

// installCapture points Enqueue at an in-memory recorder and marks
// the queue as started (so Enqueue's started-guard passes). Does NOT
// spin workers — use installRunning for tests that also want jobs
// dispatched.
func installCapture(t *testing.T) *fakeInsert {
	t.Helper()
	restoreVars(t)
	resetQueue()
	t.Cleanup(resetQueue)

	fi := &fakeInsert{}
	insertJob = fi.Fn
	countPend = func(context.Context) (int, error) { return 0, nil }

	queueMu.Lock()
	queueStart = true
	queueSoftCap = 1024
	queueMu.Unlock()
	return fi
}

// installRunning: records inserts AND invokes dispatch on each one so
// the closure side-effects (arch / code calls) are exercised. Uses
// a pass-through dispatch stub that reports ok; tests that want to
// assert on dispatch arguments should replace `dispatch` after.
func installRunning(t *testing.T) *fakeInsert {
	t.Helper()
	fi := installCapture(t)
	// Swap dispatch with a recorder that always succeeds: this both
	// provides coverage for the payload round-trip and isolates us from
	// booting arch / code.
	dispatch = func(ctx context.Context, kind string, payload json.RawMessage) error {
		return nil
	}
	// Pump inserted rows through dispatch synchronously from a goroutine.
	var pumpMu sync.Mutex
	pumped := map[int]bool{}
	origFn := fi.Fn
	insertJob = func(ctx context.Context, kind, name string, payload any) (int64, error) {
		id, err := origFn(ctx, kind, name, payload)
		if err != nil {
			return id, err
		}
		go func() {
			pumpMu.Lock()
			idx := int(id) - 1
			if pumped[idx] {
				pumpMu.Unlock()
				return
			}
			pumped[idx] = true
			pumpMu.Unlock()
			fi.mu.Lock()
			raw := fi.payloads[idx]
			fi.mu.Unlock()
			_ = dispatch(context.Background(), kind, raw)
		}()
		return id, nil
	}
	return fi
}

func TestEnqueueWithoutStart(t *testing.T) {
	resetQueue()
	// Should not panic; just logs and drops.
	Enqueue("noop", "noop", map[string]string{})
}

func TestStartIsIdempotent(t *testing.T) {
	restoreVars(t)
	resetQueue()
	defer resetQueue()

	resetRun = func(context.Context) (int64, error) { return 0, nil }
	claimJob = func(context.Context) (*data.WebhookJob, error) { return nil, nil }

	Start(context.Background(), 1, 4)
	queueMu.Lock()
	cap1 := queueSoftCap
	queueMu.Unlock()

	// Second Start is a no-op.
	Start(context.Background(), 5, 99)
	queueMu.Lock()
	cap2 := queueSoftCap
	queueMu.Unlock()
	if cap1 != cap2 {
		t.Errorf("second Start changed cap: %d -> %d", cap1, cap2)
	}
}

func TestStartDefaultsCorrectMinima(t *testing.T) {
	restoreVars(t)
	resetQueue()
	defer resetQueue()

	resetRun = func(context.Context) (int64, error) { return 0, nil }
	claimJob = func(context.Context) (*data.WebhookJob, error) { return nil, nil }

	Start(context.Background(), 0, 0)
	queueMu.Lock()
	cap := queueSoftCap
	queueMu.Unlock()
	if cap != 1 {
		t.Errorf("expected cap 1 from minima, got %d", cap)
	}
}

func TestStartReclaimsRunningRows(t *testing.T) {
	restoreVars(t)
	resetQueue()
	defer resetQueue()

	var resetCalled int32
	resetRun = func(context.Context) (int64, error) {
		atomic.StoreInt32(&resetCalled, 1)
		return 3, nil
	}
	claimJob = func(context.Context) (*data.WebhookJob, error) { return nil, nil }

	Start(context.Background(), 1, 4)
	if atomic.LoadInt32(&resetCalled) != 1 {
		t.Error("ResetRunningWebhookJobs was not called")
	}
}

func TestStartReclaimError(t *testing.T) {
	restoreVars(t)
	resetQueue()
	defer resetQueue()

	resetRun = func(context.Context) (int64, error) { return 0, errors.New("db down") }
	claimJob = func(context.Context) (*data.WebhookJob, error) { return nil, nil }

	// Must not panic even on reset error.
	Start(context.Background(), 1, 4)
}

func TestEnqueueInsertsRow(t *testing.T) {
	fi := installCapture(t)
	Enqueue("test.kind", "test.kind:ABC", map[string]string{"hello": "world"})
	if got := fi.Names(); len(got) != 1 || got[0] != "test.kind:ABC" {
		t.Errorf("names: %v", got)
	}
}

func TestEnqueueDropsOnSoftCap(t *testing.T) {
	restoreVars(t)
	resetQueue()
	defer resetQueue()

	fi := &fakeInsert{}
	insertJob = fi.Fn
	countPend = func(context.Context) (int, error) { return 10, nil }

	queueMu.Lock()
	queueStart = true
	queueSoftCap = 5
	queueMu.Unlock()

	Enqueue("test", "test", map[string]string{})
	if len(fi.Names()) != 0 {
		t.Errorf("expected drop, got insert")
	}
}

func TestEnqueueInsertError(t *testing.T) {
	restoreVars(t)
	resetQueue()
	defer resetQueue()

	fi := &fakeInsert{err: errors.New("db down")}
	insertJob = fi.Fn
	countPend = func(context.Context) (int, error) { return 0, nil }

	queueMu.Lock()
	queueStart = true
	queueSoftCap = 1024
	queueMu.Unlock()

	// Should log + swallow; no panic.
	Enqueue("test", "test", map[string]string{})
}

func TestEnqueueCountError(t *testing.T) {
	restoreVars(t)
	resetQueue()
	defer resetQueue()

	fi := &fakeInsert{}
	insertJob = fi.Fn
	// Count error should not block — Enqueue proceeds with insert.
	countPend = func(context.Context) (int, error) { return 0, errors.New("count fail") }

	queueMu.Lock()
	queueStart = true
	queueSoftCap = 5
	queueMu.Unlock()

	Enqueue("test", "test", map[string]string{})
	if len(fi.Names()) != 1 {
		t.Errorf("expected insert despite count error, got %v", fi.Names())
	}
}

func TestPollLoopRunsClaimedJob(t *testing.T) {
	restoreVars(t)
	resetQueue()
	defer resetQueue()

	// Serve exactly one job then return nil forever.
	var served int32
	claimJob = func(context.Context) (*data.WebhookJob, error) {
		if atomic.AddInt32(&served, 1) == 1 {
			return &data.WebhookJob{
				ID:      7,
				Kind:    "sample",
				Name:    "sample:PROJ-1",
				Payload: json.RawMessage(`{}`),
			}, nil
		}
		return nil, nil
	}

	var dispatched int32
	dispatch = func(ctx context.Context, kind string, payload json.RawMessage) error {
		atomic.AddInt32(&dispatched, 1)
		return nil
	}

	var doneID int64
	markDone = func(_ context.Context, id int64) error {
		atomic.StoreInt64(&doneID, id)
		return nil
	}
	markFail = func(_ context.Context, id int64, _ string) error {
		t.Errorf("unexpected markFail for id %d", id)
		return nil
	}
	resetRun = func(context.Context) (int64, error) { return 0, nil }

	Start(context.Background(), 1, 4)
	defer resetQueue()

	waitForCond(t, 2*time.Second, func() bool {
		return atomic.LoadInt32(&dispatched) == 1 && atomic.LoadInt64(&doneID) == 7
	})
}

func TestPollLoopMarksFailedOnDispatchErr(t *testing.T) {
	restoreVars(t)
	resetQueue()
	defer resetQueue()

	var served int32
	claimJob = func(context.Context) (*data.WebhookJob, error) {
		if atomic.AddInt32(&served, 1) == 1 {
			return &data.WebhookJob{ID: 8, Kind: "k", Payload: json.RawMessage(`{}`)}, nil
		}
		return nil, nil
	}
	dispatch = func(context.Context, string, json.RawMessage) error { return errors.New("boom") }

	var failMsg string
	markFail = func(_ context.Context, id int64, msg string) error {
		failMsg = msg
		return nil
	}
	markDone = func(_ context.Context, id int64) error {
		t.Errorf("unexpected markDone for %d", id)
		return nil
	}
	resetRun = func(context.Context) (int64, error) { return 0, nil }

	Start(context.Background(), 1, 4)
	defer resetQueue()

	waitForCond(t, 2*time.Second, func() bool {
		return strings.Contains(failMsg, "boom")
	})
}

func TestPollLoopRecoversPanic(t *testing.T) {
	restoreVars(t)
	resetQueue()
	defer resetQueue()

	var served int32
	claimJob = func(context.Context) (*data.WebhookJob, error) {
		if atomic.AddInt32(&served, 1) == 1 {
			return &data.WebhookJob{ID: 9, Kind: "k", Payload: json.RawMessage(`{}`)}, nil
		}
		return nil, nil
	}
	dispatch = func(context.Context, string, json.RawMessage) error { panic("kaboom") }

	var failMsg string
	markFail = func(_ context.Context, id int64, msg string) error {
		failMsg = msg
		return nil
	}
	markDone = func(_ context.Context, id int64) error { return nil }
	resetRun = func(context.Context) (int64, error) { return 0, nil }

	Start(context.Background(), 1, 4)
	defer resetQueue()

	waitForCond(t, 2*time.Second, func() bool {
		return strings.Contains(failMsg, "panic")
	})
}

func TestPollLoopClaimErrBackoff(t *testing.T) {
	restoreVars(t)
	resetQueue()
	defer resetQueue()

	var served int32
	claimJob = func(context.Context) (*data.WebhookJob, error) {
		n := atomic.AddInt32(&served, 1)
		if n <= 2 {
			return nil, errors.New("transient")
		}
		return nil, nil
	}
	resetRun = func(context.Context) (int64, error) { return 0, nil }

	Start(context.Background(), 1, 4)
	defer resetQueue()

	waitForCond(t, 2*time.Second, func() bool {
		return atomic.LoadInt32(&served) >= 2
	})
}

func TestDrainBeforeStart(t *testing.T) {
	resetQueue()
	Drain(context.Background())
}

func TestDrainStops(t *testing.T) {
	restoreVars(t)
	resetQueue()

	claimJob = func(context.Context) (*data.WebhookJob, error) { return nil, nil }
	resetRun = func(context.Context) (int64, error) { return 0, nil }

	Start(context.Background(), 1, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	Drain(ctx)
}

func TestPanicErrorFormatting(t *testing.T) {
	if panicError(errors.New("bad")).Error() != "panic: bad" {
		t.Errorf("error case wrong")
	}
	if panicError("oops").Error() != "panic: oops" {
		t.Errorf("string case wrong")
	}
	if panicError(42).Error() != "panic: unknown" {
		t.Errorf("unknown case wrong: %q", panicError(42).Error())
	}
}

func TestRunJobMarkDoneError(t *testing.T) {
	restoreVars(t)
	dispatch = func(context.Context, string, json.RawMessage) error { return nil }
	markDone = func(context.Context, int64) error { return errors.New("update failed") }
	runJob(context.Background(), 1, "kind", "name", []byte(`{}`))
}

func TestRunJobMarkFailError(t *testing.T) {
	restoreVars(t)
	dispatch = func(context.Context, string, json.RawMessage) error { return errors.New("boom") }
	markFail = func(context.Context, int64, string) error { return errors.New("update failed") }
	runJob(context.Background(), 1, "kind", "name", []byte(`{}`))
}

func TestSleepCtxCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sleepCtx(ctx, time.Minute)
}

// waitForCond polls until cond is true or deadline hits.
func waitForCond(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}
