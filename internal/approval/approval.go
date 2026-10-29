// Package approval carries the "a human answer is needed" signal from the
// harness to the active interface. With no interface attached it is a no-op.
package approval

import "sync"

// Listener is notified once per approval prompt. It must not block.
type Listener interface {
	ApprovalRequested()
}

var (
	mu       sync.RWMutex
	listener Listener
)

// Set registers the listener; nil clears it.
func Set(l Listener) {
	mu.Lock()
	defer mu.Unlock()
	listener = l
}

// Notify fires the registered listener, if any. Never blocks the caller.
func Notify() {
	mu.RLock()
	l := listener
	mu.RUnlock()

	if l != nil {
		l.ApprovalRequested()
	}
}
