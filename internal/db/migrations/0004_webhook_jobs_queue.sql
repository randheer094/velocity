ALTER TABLE webhook_jobs
    ADD COLUMN IF NOT EXISTS queue TEXT NOT NULL DEFAULT 'ops';

DROP INDEX IF EXISTS webhook_jobs_pending_idx;

CREATE INDEX IF NOT EXISTS webhook_jobs_pending_by_queue_idx
    ON webhook_jobs (queue, id)
    WHERE status = 'pending';
