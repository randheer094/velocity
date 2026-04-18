package webhook

import (
	"context"
	"log/slog"
	"sync"
)

// Job is one unit of queued work. Fn receives the queue's root ctx
// so shutdown cancellation propagates.
type Job struct {
	Name string
	Fn   func(ctx context.Context)
}

var (
	queueMu  sync.Mutex
	queue    chan Job
	queueCap int
	rootCtx  context.Context
	workerWg sync.WaitGroup
)

// Start boots the FIFO queue. Call once from server.Run.
func Start(ctx context.Context, workers, size int) {
	if workers < 1 {
		workers = 1
	}
	if size < 1 {
		size = 1
	}
	queueMu.Lock()
	if queue != nil {
		queueMu.Unlock()
		return
	}
	rootCtx = ctx
	queue = make(chan Job, size)
	queueCap = size
	queueMu.Unlock()

	for i := 0; i < workers; i++ {
		workerWg.Add(1)
		go worker()
	}
	slog.Info("workqueue started", "workers", workers, "size", size)
}

// Enqueue drops + logs on a full queue rather than blocking the handler.
func Enqueue(j Job) {
	queueMu.Lock()
	q := queue
	cap := queueCap
	queueMu.Unlock()
	if q == nil {
		slog.Error("workqueue not started, dropping job", "name", j.Name)
		return
	}
	select {
	case q <- j:
		slog.Info("workqueue: enqueued", "name", j.Name, "depth", len(q))
	default:
		slog.Error("workqueue full, dropping job", "name", j.Name, "size", cap)
	}
}

// Drain closes the queue and waits for workers. Returns early if ctx
// fires first (shutdown's 10 s budget).
func Drain(ctx context.Context) {
	queueMu.Lock()
	q := queue
	queue = nil
	queueMu.Unlock()
	if q == nil {
		return
	}
	close(q)
	done := make(chan struct{})
	go func() { workerWg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func worker() {
	defer workerWg.Done()
	for j := range receive() {
		runJob(j)
	}
}

func receive() chan Job {
	queueMu.Lock()
	defer queueMu.Unlock()
	return queue
}

func runJob(j Job) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("workqueue: job panic", "name", j.Name, "panic", r)
		}
	}()
	ctx := rootCtx
	if ctx == nil {
		ctx = context.Background()
	}
	j.Fn(ctx)
}
