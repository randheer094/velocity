package webhook

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/randheer094/velocity/internal/db"
)

var (
	queueMu      sync.Mutex
	queueStart   bool
	pollCtx      context.Context
	pollCancel   context.CancelFunc
	workerWg     sync.WaitGroup
	queueSoftCap int
)

// pollInterval is the sleep between claim attempts when the queue is
// empty. Short enough that a freshly-inserted job runs quickly; long
// enough that an idle daemon doesn't hammer Postgres.
const pollInterval = 500 * time.Millisecond

// opsWorkers is the hard-coded concurrency for the ops queue. The ops
// queue carries short DB/Jira/GitHub steps plus workspace cleanup;
// serializing to one worker removes ops-vs-ops races by construction.
const opsWorkers = 1

// Insertion + claim helpers are vars so tests can stub them without a
// live Postgres. Production wiring lives in internal/db.
var (
	insertJob = db.InsertWebhookJob
	claimJob  = db.ClaimNextWebhookJob
	markDone  = db.MarkWebhookJobDone
	markFail  = db.MarkWebhookJobFailed
	resetRun  = db.ResetRunningWebhookJobs
	countPend = db.CountPendingWebhookJobs
)

// Start boots the DB-backed queues. Call once from server.Run after
// db.Start. Idempotent: the second call is a no-op.
//
// Two pools run side by side:
//
//   - LLM queue: llmWorkers goroutines (cfg.Server.MaxConcurrency).
//     Carries arch.Run, code.Run, code.Iterate.
//   - Ops queue: exactly one goroutine. Carries every other kind —
//     short DB/Jira/GitHub steps and workspace cleanup.
//
// The soft cap applies per queue so a flooded ops backlog can't
// starve LLM enqueues.
//
// On first call we reset any stale `running` rows to `pending` across
// both queues — those came from a previous daemon that died mid-job,
// and are safe to replay because every agent entry re-reads DB state
// and no-ops when the ticket is already terminal.
func Start(ctx context.Context, llmWorkers, queueSize int) {
	if llmWorkers < 1 {
		llmWorkers = 1
	}
	if queueSize < 1 {
		queueSize = 1
	}

	queueMu.Lock()
	if queueStart {
		queueMu.Unlock()
		return
	}
	queueStart = true
	queueSoftCap = queueSize
	pollCtx, pollCancel = context.WithCancel(ctx)
	queueMu.Unlock()

	if n, err := resetRun(pollCtx); err != nil {
		slog.Error("workqueue: reset running failed", "err", err)
	} else if n > 0 {
		slog.Warn("workqueue: reclaimed interrupted jobs", "count", n)
	}

	for i := 0; i < llmWorkers; i++ {
		workerWg.Add(1)
		go pollLoop(pollCtx, QueueLLM)
	}
	for i := 0; i < opsWorkers; i++ {
		workerWg.Add(1)
		go pollLoop(pollCtx, QueueOps)
	}
	slog.Info("workqueue started",
		"llm_workers", llmWorkers, "ops_workers", opsWorkers, "soft_cap", queueSize)
}

// Enqueue persists a job row on the queue implied by its kind. Drops +
// logs when the pending backlog for that queue exceeds the soft cap,
// preserving the "never block the webhook handler" contract.
func Enqueue(kind, name string, payload any) {
	queueMu.Lock()
	started := queueStart
	cap := queueSoftCap
	queueMu.Unlock()
	if !started {
		slog.Error("workqueue not started, dropping job", "kind", kind, "name", name)
		return
	}

	queue := QueueForKind(kind)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if cap > 0 {
		if pending, err := countPend(ctx, queue); err == nil && pending >= cap {
			slog.Error("workqueue full, dropping job",
				"queue", queue, "kind", kind, "name", name,
				"pending", pending, "cap", cap)
			return
		}
	}

	id, err := insertJob(ctx, queue, kind, name, payload)
	if err != nil {
		slog.Error("workqueue: insert failed", "queue", queue, "kind", kind, "name", name, "err", err)
		return
	}
	slog.Info("workqueue: enqueued", "id", id, "queue", queue, "kind", kind, "name", name)
}

// Drain cancels the pollers and waits for in-flight jobs. Returns
// early if ctx fires first (shutdown budget).
func Drain(ctx context.Context) {
	queueMu.Lock()
	cancel := pollCancel
	started := queueStart
	queueStart = false
	pollCtx = nil
	pollCancel = nil
	queueSoftCap = 0
	queueMu.Unlock()
	if !started {
		return
	}
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() { workerWg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func pollLoop(ctx context.Context, queue string) {
	defer workerWg.Done()
	for {
		if ctx.Err() != nil {
			return
		}
		job, err := claimJob(ctx, queue)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("workqueue: claim failed", "queue", queue, "err", err)
			sleepCtx(ctx, pollInterval)
			continue
		}
		if job == nil {
			sleepCtx(ctx, pollInterval)
			continue
		}
		runJob(ctx, job.ID, job.Kind, job.Name, job.Payload)
	}
}

// runJob is split out so tests can drive it directly. It owns the
// panic recovery and the success/failure bookkeeping on the row.
func runJob(ctx context.Context, id int64, kind, name string, payload []byte) {
	slog.Info("workqueue: run", "id", id, "kind", kind, "name", name)
	var dispatchErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				dispatchErr = panicError(r)
				slog.Error("workqueue: job panic", "id", id, "name", name, "panic", r)
			}
		}()
		dispatchErr = dispatch(ctx, kind, payload)
	}()

	// Use a detached context for the bookkeeping UPDATE so a canceled
	// shutdown context doesn't leave the row stuck in 'running'.
	doneCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if dispatchErr != nil {
		slog.Error("workqueue: job failed", "id", id, "kind", kind, "name", name, "err", dispatchErr)
		if err := markFail(doneCtx, id, dispatchErr.Error()); err != nil {
			slog.Error("workqueue: mark failed update failed", "id", id, "err", err)
		}
		return
	}
	if err := markDone(doneCtx, id); err != nil {
		slog.Error("workqueue: mark done update failed", "id", id, "err", err)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

type panicErr struct{ v any }

func (e panicErr) Error() string { return panicString(e.v) }

func panicError(v any) error { return panicErr{v: v} }

func panicString(v any) string {
	switch x := v.(type) {
	case error:
		return "panic: " + x.Error()
	case string:
		return "panic: " + x
	default:
		return "panic: unknown"
	}
}
