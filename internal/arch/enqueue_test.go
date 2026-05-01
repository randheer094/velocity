package arch

import (
	"sync"
	"testing"
)

// enqCapture is a test recorder that swaps in for EnqueueFn so tests
// can assert on follow-up enqueues without booting the webhook queue.
type enqCapture struct {
	mu     sync.Mutex
	rows   []enqRow
	prev   func(string, string, any) bool
	stored bool
}

type enqRow struct {
	Kind    string
	Name    string
	Payload any
}

func captureEnqueue(t *testing.T) *enqCapture {
	t.Helper()
	c := &enqCapture{prev: EnqueueFn}
	EnqueueFn = func(kind, name string, payload any) bool {
		c.mu.Lock()
		c.rows = append(c.rows, enqRow{Kind: kind, Name: name, Payload: payload})
		c.mu.Unlock()
		return true
	}
	c.stored = true
	t.Cleanup(func() {
		if c.stored {
			EnqueueFn = c.prev
		}
	})
	return c
}

func (c *enqCapture) all() []enqRow {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]enqRow, len(c.rows))
	copy(out, c.rows)
	return out
}

func (c *enqCapture) kinds() []string {
	rows := c.all()
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Kind
	}
	return out
}

func (c *enqCapture) has(kind string) bool {
	for _, r := range c.all() {
		if r.Kind == kind {
			return true
		}
	}
	return false
}

func (c *enqCapture) count(kind string) int {
	n := 0
	for _, r := range c.all() {
		if r.Kind == kind {
			n++
		}
	}
	return n
}
