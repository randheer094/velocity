package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/randheer094/velocity/internal/data"
)

// InsertWebhookJob adds a pending row to the queue and returns its id.
// Payload is marshalled to JSON; callers should pass a typed struct.
func InsertWebhookJob(ctx context.Context, kind, name string, payload any) (int64, error) {
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
		INSERT INTO webhook_jobs (kind, name, payload)
		VALUES ($1, $2, $3)
		RETURNING id
	`, kind, name, raw).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// ClaimNextWebhookJob returns the oldest pending job and marks it
// running in a single statement. Safe for concurrent workers:
// FOR UPDATE SKIP LOCKED guarantees each row is handed to exactly
// one caller. Returns (nil, nil) when the queue is empty.
func ClaimNextWebhookJob(ctx context.Context) (*data.WebhookJob, error) {
	p := Shared()
	if p == nil {
		return nil, ErrNotStarted
	}
	var j data.WebhookJob
	var statusStr string
	err := p.QueryRow(ctx, `
		WITH next AS (
			SELECT id FROM webhook_jobs
			WHERE status = 'pending'
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
		RETURNING j.id, j.kind, j.name, j.payload, j.status, j.attempts,
		          j.last_error, j.enqueued_at, j.started_at, j.finished_at
	`).Scan(
		&j.ID, &j.Kind, &j.Name, &j.Payload, &statusStr, &j.Attempts,
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

// CountPendingWebhookJobs backs the soft-cap check in webhook.Enqueue.
func CountPendingWebhookJobs(ctx context.Context) (int, error) {
	p := Shared()
	if p == nil {
		return 0, ErrNotStarted
	}
	var n int
	err := p.QueryRow(ctx, `SELECT COUNT(*) FROM webhook_jobs WHERE status = 'pending'`).Scan(&n)
	return n, err
}
