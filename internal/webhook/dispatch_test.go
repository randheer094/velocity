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
		KindArchOnDismissed: "arch.OnDismissed",
		KindCodeRun:         "code.Run",
		KindCodeMarkMerged:  "code.MarkMerged",
		KindCodeIterate:     "code.Iterate",
		KindCodeOnDismissed: "code.OnDismissed",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("kind constant drift: got %q want %q", got, want)
		}
	}
}
