// Package approval carries the "a human answer is needed" signal from the
// harness to whichever interface is in front of the user.
//
// The harness (harnessPermissions) knows nothing about the TUI, and the TUI
// knows nothing about who is asking: both talk to this package. A run with no
// interface attached — the plain CLI path, or any reuse of the harness outside
// optine's TUI — is simply a no-op.
package approval

import "sync"

// Listener is told, once per approval prompt, that the user is being asked a
// question right now. Implementations must not block: this fires on the
// harness's goroutine while it waits for an answer.
type Listener interface {
	ApprovalRequested()
}

var (
	mu       sync.RWMutex
	listener Listener
)

// Set registers the listener. Pass nil to clear it — tui.Run does this on exit
// so a second run in the same process never double-notifies.
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
