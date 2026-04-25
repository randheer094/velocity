package data

import (
	"encoding/json"
	"time"
)

type WebhookJobStatus string

const (
	WebhookJobPending WebhookJobStatus = "pending"
	WebhookJobRunning WebhookJobStatus = "running"
	WebhookJobDone    WebhookJobStatus = "done"
	WebhookJobFailed  WebhookJobStatus = "failed"
)

// WebhookJob is a persisted unit of queued work. `Kind` routes to a
// dispatcher; `Payload` carries the JSON-encoded args for that kind.
type WebhookJob struct {
	ID          int64            `json:"id"`
	Queue       string           `json:"queue"`
	Kind        string           `json:"kind"`
	Name        string           `json:"name,omitempty"`
	Payload     json.RawMessage  `json:"payload"`
	Status      WebhookJobStatus `json:"status"`
	Attempts    int              `json:"attempts"`
	LastError   string           `json:"last_error,omitempty"`
	EnqueuedAt  time.Time        `json:"enqueued_at"`
	StartedAt   *time.Time       `json:"started_at,omitempty"`
	FinishedAt  *time.Time       `json:"finished_at,omitempty"`
}
