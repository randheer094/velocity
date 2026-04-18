package jira

import "sync"

var (
	shared   *Client
	sharedMu sync.RWMutex
)

// Reinit rebuilds the shared client; call after setup saves new creds.
func Reinit() {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	shared = New()
}

func Shared() *Client {
	sharedMu.RLock()
	defer sharedMu.RUnlock()
	return shared
}
