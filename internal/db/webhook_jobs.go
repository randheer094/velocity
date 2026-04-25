package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/randheer094/velocity/internal/data"
)

// InsertWebhookJob adds a pending row to the given queue and returns its id.
// Payload is marshalled to JSON; callers should pass a typed struct.
func InsertWebhookJob(ctx context.Context, queue, kind, name string, payload any) (int64, error) {
	p := Shared()
	if p == nil {
		return 0, ErrNotStarted
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("marshal payload: %w", err)
	}
	var id int64
	err = p.QueryRow(ctx, `
		INSERT INTO webhook_jobs (queue, kind, name, payload)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, queue, kind, name, raw).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// ClaimNextWebhookJob returns the oldest pending job for the given queue
// and marks it running in a single statement. Safe for concurrent workers:
// FOR UPDATE SKIP LOCKED guarantees each row is handed to exactly one
// caller. Returns (nil, nil) when the queue is empty.
func ClaimNextWebhookJob(ctx context.Context, queue string) (*data.WebhookJob, error) {
	p := Shared()
	if p == nil {
		return nil, ErrNotStarted
	}
	var j data.WebhookJob
	var statusStr string
	err := p.QueryRow(ctx, `
		WITH next AS (
			SELECT id FROM webhook_jobs
			WHERE status = 'pending' AND queue = $1
			ORDER BY id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE webhook_jobs j
		SET status = 'running',
		    started_at = NOW(),
		    attempts = j.attempts + 1
		FROM next
		WHERE j.id = next.id
		RETURNING j.id, j.queue, j.kind, j.name, j.payload, j.status, j.attempts,
		          j.last_error, j.enqueued_at, j.started_at, j.finished_at
	`, queue).Scan(
		&j.ID, &j.Queue, &j.Kind, &j.Name, &j.Payload, &statusStr, &j.Attempts,
		&j.LastError, &j.EnqueuedAt, &j.StartedAt, &j.FinishedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	j.Status = data.WebhookJobStatus(statusStr)
	return &j, nil
}

// MarkWebhookJobDone flips a running row to done.
func MarkWebhookJobDone(ctx context.Context, id int64) error {
	p := Shared()
	if p == nil {
		return ErrNotStarted
	}
	_, err := p.Exec(ctx, `
		UPDATE webhook_jobs
		SET status = 'done', finished_at = NOW()
		WHERE id = $1
	`, id)
	return err
}

// MarkWebhookJobFailed records a terminal failure; the operator-visible
// retry path (reassign the ticket) continues to work via the agent guards.
func MarkWebhookJobFailed(ctx context.Context, id int64, errMsg string) error {
	p := Shared()
	if p == nil {
		return ErrNotStarted
	}
	_, err := p.Exec(ctx, `
		UPDATE webhook_jobs
		SET status = 'failed', finished_at = NOW(), last_error = $2
		WHERE id = $1
	`, id, errMsg)
	return err
}

// ResetRunningWebhookJobs is called once on daemon start: rows marked
// running must have been interrupted by a crash (only this process
// ever writes 'running'). Flipping them back to pending lets the
// workers pick them up again. Agent entries are idempotent.
func ResetRunningWebhookJobs(ctx context.Context) (int64, error) {
	p := Shared()
	if p == nil {
		return 0, ErrNotStarted
	}
	tag, err := p.Exec(ctx, `
		UPDATE webhook_jobs
		SET status = 'pending', started_at = NULL
		WHERE status = 'running'
	`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// CountPendingWebhookJobs returns the pending backlog for the given queue;
// backs the soft-cap check in webhook.Enqueue.
func CountPendingWebhookJobs(ctx context.Context, queue string) (int, error) {
	p := Shared()
	if p == nil {
		return 0, ErrNotStarted
	}
	var n int
	err := p.QueryRow(ctx,
		`SELECT COUNT(*) FROM webhook_jobs WHERE status = 'pending' AND queue = $1`,
		queue,
	).Scan(&n)
	return n, err
}
