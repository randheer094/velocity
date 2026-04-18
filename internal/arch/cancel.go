package arch

import (
	"context"
	"sync"
)

var (
	cancelsMu sync.Mutex
	cancels   = map[string]context.CancelFunc{}
)

func registerCancel(key string, cancel context.CancelFunc) {
	cancelsMu.Lock()
	defer cancelsMu.Unlock()
	if prev, ok := cancels[key]; ok {
		prev()
	}
	cancels[key] = cancel
}

func unregisterCancel(key string) {
	cancelsMu.Lock()
	defer cancelsMu.Unlock()
	delete(cancels, key)
}

func cancelIfRunning(key string) {
	cancelsMu.Lock()
	cancel := cancels[key]
	delete(cancels, key)
	cancelsMu.Unlock()
	if cancel != nil {
		cancel()
	}
}
