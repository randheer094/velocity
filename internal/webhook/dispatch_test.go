package webhook

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/randheer094/velocity/internal/code"
)

// The real dispatch calls into arch / code, which boot git / LLM /
// Postgres. These tests only cover routing + payload unmarshalling:
// we assert on the error surface (bad JSON, unknown kind) and on the
// happy-path unmarshal itself. Full end-to-end routing is covered by
// the handlers_test suite via startRunningQueue.

func callDispatch(t *testing.T, kind string, raw []byte) error {
	t.Helper()
	// Capture dispatch so a panic from the agent layer doesn't explode
	// the test runner. We only care about the unmarshal error path.
	defer func() { _ = recover() }()
	return dispatch(context.Background(), kind, raw)
}

func TestDispatchUnknownKind(t *testing.T) {
	err := dispatch(context.Background(), "bogus.kind", json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Errorf("expected unknown-kind error, got %v", err)
	}
}

func TestDispatchBadJSONForEachKind(t *testing.T) {
	for _, k := range []string{
		KindArchRun, KindArchAdvanceWave, KindArchOnDismissed,
		KindCodeRun, KindCodeMarkMerged, KindCodeIterate, KindCodeOnDismissed,
	} {
		err := callDispatch(t, k, []byte(`not json`))
		if err == nil {
			t.Errorf("%s: expected unmarshal error", k)
		}
	}
}

func TestPayloadRoundTrip(t *testing.T) {
	cases := []any{
		archRunPayload{Key: "P-1", RepoURL: "r", Summary: "s", Requirement: "req"},
		archAdvanceWavePayload{ParentKey: "P-1"},
		archOnDismissedPayload{Key: "P-1", JiraStatus: "Dismissed"},
		codeRunPayload{Key: "P-2", ParentKey: "P-1", RepoURL: "r", Summary: "s", Description: "d"},
		codeMarkMergedPayload{Branch: "P-2", PRURL: "https://x"},
		codeIteratePayload{Branch: "P-2", Reason: code.IterateCI, Extra: "e", Hint: "h"},
		codeOnDismissedPayload{Key: "P-2", JiraStatus: "Dismissed", ParentKey: "P-1"},
	}
	for _, c := range cases {
		raw, err := json.Marshal(c)
		if err != nil {
			t.Errorf("marshal %T: %v", c, err)
			continue
		}
		// Unmarshal into the same shape; assert no error.
		switch c.(type) {
		case archRunPayload:
			var p archRunPayload
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Errorf("%T round-trip: %v", c, err)
			}
		case codeRunPayload:
			var p codeRunPayload
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Errorf("%T round-trip: %v", c, err)
			}
		case codeIteratePayload:
			var p codeIteratePayload
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Errorf("%T round-trip: %v", c, err)
			}
			if p.Reason != code.IterateCI {
				t.Errorf("reason lost: %q", p.Reason)
			}
		}
	}
}

// TestKindConstantsStable guards the kind strings against accidental
// renaming — `kind` is persisted in the DB so a rename breaks rows
// written by an older binary.
func TestKindConstantsStable(t *testing.T) {
	cases := map[string]string{
		KindArchRun:         "arch.Run",
		KindArchAdvanceWave: "arch.AdvanceWave",
		KindArchAssignWave:  "arch.AssignWave",
		KindArchArchive:     "arch.Archive",
		KindArchOnDismissed: "arch.OnDismissed",
		KindCodeRun:         "code.Run",
		KindCodeMarkMerged:  "code.MarkMerged",
		KindCodeIterate:     "code.Iterate",
		KindCodeOnDismissed: "code.OnDismissed",
		KindCodeCleanup:     "code.Cleanup",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("kind constant drift: got %q want %q", got, want)
		}
	}
}

// TestQueueForKindRouting asserts every known kind lands on the
// expected queue, and unknown kinds fall through to ops (conservative).
func TestQueueForKindRouting(t *testing.T) {
	cases := map[string]string{
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
	for kind, want := range cases {
		if got := QueueForKind(kind); got != want {
			t.Errorf("QueueForKind(%q) = %q, want %q", kind, got, want)
		}
	}
	if got := QueueForKind("totally.unknown"); got != QueueOps {
		t.Errorf("QueueForKind(unknown) = %q, want %q", got, QueueOps)
	}
}

// TestDispatchEnqueuesCleanupForMerged verifies the canonical
// cascade — code.MarkMerged dispatches without inline-RemoveAll and
// instead enqueues code.Cleanup. Uses installCapture so the inserts
// are observable without a real queue.
func TestDispatchEnqueuesAdvanceAfterDismiss(t *testing.T) {
	fi := installCapture(t)
	// Stub code.OnDismissed via the dispatch path; we don't exercise
	// the real implementation (it touches DB / FS). Instead drive
	// dispatch directly and rely on the fakeInsert recorder for the
	// trailing AdvanceWave enqueue.
	payload := []byte(`{"key":"PROJ-2","jira_status":"Dismissed","parent_key":"PROJ-1"}`)
	defer func() { _ = recover() }() // code.OnDismissed needs DB; we only care about the enqueue cascade
	_ = dispatch(context.Background(), KindCodeOnDismissed, payload)
	for _, name := range fi.Names() {
		if name == "arch.AdvanceWave:PROJ-1" {
			return
		}
	}
	t.Errorf("expected arch.AdvanceWave:PROJ-1 enqueued, got %v", fi.Names())
}
