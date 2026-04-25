package arch

// EnqueueFn is the seam through which arch hands follow-up work back
// to the webhook queue. server.Run wires it to webhook.Enqueue;
// pre-startup it's a no-op so unit tests that don't care about
// queueing can ignore it. Keeps arch from importing webhook (and
// closing the import cycle that webhook → arch already opens).
var EnqueueFn = func(kind, name string, payload any) {}

// Kind strings for follow-ups arch enqueues. Mirror of the canonical
// definitions in internal/webhook/dispatch.go; kept private to arch
// so the agent doesn't accidentally start importing the webhook
// package. TestEnqueueKindStringsMatchWebhook in the webhook package
// guards the match.
const (
	kindAdvanceWave = "arch.AdvanceWave"
	kindAssignWave  = "arch.AssignWave"
	kindArchive     = "arch.Archive"
)

// IsInFlight reports whether a Run is currently planning parentKey.
// Ops handlers that touch the parent's workspace (Archive) read this
// to avoid clobbering an in-flight clone.
func IsInFlight(parentKey string) bool {
	inFlightMu.Lock()
	defer inFlightMu.Unlock()
	_, ok := inFlight[parentKey]
	return ok
}
