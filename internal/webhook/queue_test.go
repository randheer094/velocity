package webhook

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func resetQueue() {
	queueMu.Lock()
	q := queue
	queueMu.Unlock()
	if q != nil {
		defer func() { _ = recover() }()
		close(q)
	}
	workerWg.Wait()
	queueMu.Lock()
	queue = nil
	queueCap = 0
	rootCtx = nil
	queueMu.Unlock()
}

func TestEnqueueWithoutStart(t *testing.T) {
	resetQueue()
	// Should not panic; just logs and drops.
	Enqueue(Job{Name: "noop", Fn: func(ctx context.Context) {}})
}

func TestStartRunsJobs(t *testing.T) {
	resetQueue()
	defer resetQueue()
	Start(context.Background(), 2, 4)

	var wg sync.WaitGroup
	var ran int32
	for i := 0; i < 3; i++ {
		wg.Add(1)
		Enqueue(Job{Name: "j", Fn: func(ctx context.Context) {
			atomic.AddInt32(&ran, 1)
			wg.Done()
		}})
	}
	wg.Wait()
	if got := atomic.LoadInt32(&ran); got != 3 {
		t.Errorf("expected 3 jobs ran, got %d", got)
	}
}

func TestStartIsIdempotent(t *testing.T) {
	resetQueue()
	defer resetQueue()
	Start(context.Background(), 1, 1)
	// second Start is no-op
	Start(context.Background(), 5, 5)
	queueMu.Lock()
	cap := queueCap
	queueMu.Unlock()
	if cap != 1 {
		t.Errorf("Start not idempotent, cap=%d", cap)
	}
}

func TestStartDefaultsCorrectMinima(t *testing.T) {
	resetQueue()
	defer resetQueue()
	Start(context.Background(), 0, 0)
	queueMu.Lock()
	cap := queueCap
	queueMu.Unlock()
	if cap != 1 {
		t.Errorf("expected cap 1 from minima, got %d", cap)
	}
}

func TestEnqueueDropOnFull(t *testing.T) {
	resetQueue()
	defer resetQueue()

	// Use a single worker; have it block briefly so we can fill the queue.
	block := make(chan struct{})
	release := make(chan struct{})
	Start(context.Background(), 1, 1)

	Enqueue(Job{Name: "block", Fn: func(ctx context.Context) {
		close(block)
		<-release
	}})
	<-block

	// One slot in the queue; fill it
	Enqueue(Job{Name: "queued", Fn: func(ctx context.Context) {}})
	// This should be dropped, no panic
	Enqueue(Job{Name: "dropped", Fn: func(ctx context.Context) {}})

	close(release)
	// Drain to clean up
	Drain(context.Background())
}

func TestRunJobRecoversPanic(t *testing.T) {
	resetQueue()
	defer resetQueue()
	Start(context.Background(), 1, 1)

	done := make(chan struct{})
	Enqueue(Job{Name: "panicker", Fn: func(ctx context.Context) {
		defer close(done)
		panic("boom")
	}})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("panic job didn't run")
	}
}

func TestDrainBeforeStart(t *testing.T) {
	resetQueue()
	Drain(context.Background())
}

func TestDrainStops(t *testing.T) {
	resetQueue()
	defer resetQueue()
	Start(context.Background(), 1, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	Drain(ctx)
}

func TestRunJobWithNilCtx(t *testing.T) {
	resetQueue()
	defer resetQueue()
	// Don't call Start; rootCtx stays nil.
	rootCtx = nil
	done := make(chan struct{})
	go func() {
		runJob(Job{Name: "x", Fn: func(ctx context.Context) {
			if ctx == nil {
				t.Errorf("expected fallback ctx")
			}
			close(done)
		}})
	}()
	<-done
}
