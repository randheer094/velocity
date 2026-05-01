package code

// EnqueueFn is the seam through which code hands follow-up work back
// to the webhook queue. server.Run wires it to webhook.Enqueue;
// pre-startup it's a no-op so unit tests that don't care about
// queueing can ignore it. Keeps code from importing webhook (which
// already imports code). Returns true on a successful enqueue so
// callers can fall back to a synchronous step when the queue is
// full or down.
var EnqueueFn = func(kind, name string, payload any) bool { return false }

// Kind string for the cleanup follow-up code enqueues. Mirror of
// the canonical KindCodeCleanup definition in
// internal/webhook/dispatch.go.
const kindCleanup = "code.Cleanup"

// IsInFlight reports whether a Run or Iterate is currently using
// issueKey's workspace. The ops-side Cleanup handler reads this so
// it doesn't yank files out from under a still-running LLM step.
func IsInFlight(issueKey string) bool {
	inFlightMu.Lock()
	defer inFlightMu.Unlock()
	_, ok := inFlight[issueKey]
	return ok
}
