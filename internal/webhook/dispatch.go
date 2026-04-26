package webhook

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/randheer094/velocity/internal/arch"
	"github.com/randheer094/velocity/internal/code"
)

// QueueLLM carries LLM-bound agent entries (long-running, bounded by
// cfg.Server.MaxConcurrency). QueueOps is strictly serialized (1 worker)
// and carries short DB/Jira/GitHub steps plus workspace cleanup.
const (
	QueueLLM = "llm"
	QueueOps = "ops"
)

// Kind constants identify which agent entry point a queued job drives.
// Persisted as the `kind` column — renaming is a breaking change.
const (
	KindArchRun         = "arch.Run"
	KindArchAdvanceWave = "arch.AdvanceWave"
	KindArchAssignWave  = "arch.AssignWave"
	KindArchArchive     = "arch.Archive"
	KindArchOnDismissed = "arch.OnDismissed"
	KindCodeRun         = "code.Run"
	KindCodeIterate     = "code.Iterate"
	KindCodeMarkMerged  = "code.MarkMerged"
	KindCodeOnDismissed = "code.OnDismissed"
	KindCodeCleanup     = "code.Cleanup"
)

// kindQueue maps a kind to its queue. Unknown kinds fall through to
// QueueOps — a fresh deployment that lands a row for an unrecognised
// kind won't silently burn LLM worker slots.
var kindQueue = map[string]string{
	KindArchRun:         QueueLLM,
	KindCodeRun:         QueueLLM,
	KindCodeIterate:     QueueLLM,
	KindArchAdvanceWave: QueueOps,
	KindArchAssignWave:  QueueOps,
	KindArchArchive:     QueueOps,
	KindArchOnDismissed: QueueOps,
	KindCodeMarkMerged:  QueueOps,
	KindCodeOnDismissed: QueueOps,
	KindCodeCleanup:     QueueOps,
}

// QueueForKind returns the queue name for the given kind. Defaults to
// QueueOps so a mis-configured kind is serialized rather than parallel.
func QueueForKind(kind string) string {
	if q, ok := kindQueue[kind]; ok {
		return q
	}
	return QueueOps
}

type archRunPayload struct {
	Key         string `json:"key"`
	RepoURL     string `json:"repo_url"`
	Summary     string `json:"summary"`
	Requirement string `json:"requirement"`
}

type archAdvanceWavePayload struct {
	ParentKey string `json:"parent_key"`
}

type archAssignWavePayload struct {
	ParentKey string `json:"parent_key"`
	WaveIdx   int    `json:"wave_idx"`
}

type archArchivePayload struct {
	ParentKey string `json:"parent_key"`
}

type archOnDismissedPayload struct {
	Key        string `json:"key"`
	JiraStatus string `json:"jira_status"`
}

type codeRunPayload struct {
	Key         string `json:"key"`
	ParentKey   string `json:"parent_key"`
	RepoURL     string `json:"repo_url"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
}

type codeMarkMergedPayload struct {
	Branch string `json:"branch"`
	PRURL  string `json:"pr_url"`
}

type codeIteratePayload struct {
	RepoURL string             `json:"repo_url"`
	Branch  string             `json:"branch"`
	PRURL   string             `json:"pr_url"`
	Reason  code.IterateReason `json:"reason"`
	Extra   string             `json:"extra"`
	Hint    string             `json:"hint"`
}

// codeOnDismissedPayload carries ParentKey so the handler can enqueue
// the parent's AdvanceWave follow-up — one step per event, no inline
// cascade into arch.
type codeOnDismissedPayload struct {
	Key        string `json:"key"`
	JiraStatus string `json:"jira_status"`
	ParentKey  string `json:"parent_key,omitempty"`
}

type codeCleanupPayload struct {
	IssueKey string `json:"issue_key"`
}

// dispatch routes one claimed job to its agent entry. Returns an
// error when the payload is malformed or an agent call returns one;
// a non-nil error means the queue row should be marked failed.
// Agent entries that return no error (arch.Run, code.Run,
// code.Iterate) are treated as fire-and-forget: their internal
// failure recorder already wrote Jira/DB state, so the queue row
// can be marked done regardless.
var dispatch = func(ctx context.Context, kind string, payload json.RawMessage) error {
	switch kind {
	case KindArchRun:
		var p archRunPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return fmt.Errorf("%s: %w", kind, err)
		}
		arch.Run(ctx, p.Key, p.RepoURL, p.Summary, p.Requirement)
		return nil

	case KindArchAdvanceWave:
		var p archAdvanceWavePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return fmt.Errorf("%s: %w", kind, err)
		}
		return arch.AdvanceWave(ctx, p.ParentKey)

	case KindArchAssignWave:
		var p archAssignWavePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return fmt.Errorf("%s: %w", kind, err)
		}
		return arch.AssignWave(ctx, p.ParentKey, p.WaveIdx)

	case KindArchArchive:
		var p archArchivePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return fmt.Errorf("%s: %w", kind, err)
		}
		return arch.Archive(ctx, p.ParentKey)

	case KindArchOnDismissed:
		var p archOnDismissedPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return fmt.Errorf("%s: %w", kind, err)
		}
		return arch.OnDismissed(ctx, p.Key, p.JiraStatus)

	case KindCodeRun:
		var p codeRunPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return fmt.Errorf("%s: %w", kind, err)
		}
		code.Run(ctx, p.Key, p.ParentKey, p.RepoURL, p.Summary, p.Description)
		return nil

	case KindCodeMarkMerged:
		var p codeMarkMergedPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return fmt.Errorf("%s: %w", kind, err)
		}
		return code.MarkMerged(ctx, p.Branch, p.PRURL)

	case KindCodeIterate:
		var p codeIteratePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return fmt.Errorf("%s: %w", kind, err)
		}
		code.Iterate(ctx, p.RepoURL, p.Branch, p.PRURL, p.Reason, p.Extra, p.Hint)
		return nil

	case KindCodeOnDismissed:
		var p codeOnDismissedPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return fmt.Errorf("%s: %w", kind, err)
		}
		if err := code.OnDismissed(ctx, p.Key, p.JiraStatus); err != nil {
			return err
		}
		if p.ParentKey != "" {
			Enqueue(KindArchAdvanceWave, "arch.AdvanceWave:"+p.ParentKey, archAdvanceWavePayload{
				ParentKey: p.ParentKey,
			})
		}
		return nil

	case KindCodeCleanup:
		var p codeCleanupPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return fmt.Errorf("%s: %w", kind, err)
		}
		return code.Cleanup(ctx, p.IssueKey)

	default:
		return fmt.Errorf("unknown webhook job kind: %s", kind)
	}
}
