CREATE TABLE IF NOT EXISTS webhook_jobs (
    id           BIGSERIAL   PRIMARY KEY,
    kind         TEXT        NOT NULL,
    name         TEXT        NOT NULL DEFAULT '',
    payload      JSONB       NOT NULL,
    status       TEXT        NOT NULL DEFAULT 'pending',
    attempts     INT         NOT NULL DEFAULT 0,
    last_error   TEXT        NOT NULL DEFAULT '',
    enqueued_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at   TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS webhook_jobs_pending_idx
    ON webhook_jobs (id)
    WHERE status = 'pending';
