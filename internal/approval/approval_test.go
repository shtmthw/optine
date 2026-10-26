package approval

import "testing"

// fakeListener counts ApprovalRequested calls.
type fakeListener struct{ calls int }

func (f *fakeListener) ApprovalRequested() { f.calls++ }

// Notify with no listener registered must be a harmless no-op: the plain CLI
// path and any reuse of the harness outside the TUI register nothing.
func TestNotifyWithoutListenerIsNoOp(t *testing.T) {
	Set(nil)
	Notify() // must not panic
}

// Set registers the listener, Notify fires it exactly once per call, and a
// later Set(nil) clears it so a second run never double-notifies.
func TestSetNotifyAndClear(t *testing.T) {
	l := &fakeListener{}
	Set(l)

	Notify()
	Notify()
	if l.calls != 2 {
		t.Fatalf("want 2 notifications, got %d", l.calls)
	}

	Set(nil)
	Notify()
	if l.calls != 2 {
		t.Fatalf("cleared listener must not fire again, got %d", l.calls)
	}
}

// Set replaces the previous listener rather than appending a second one.
func TestSetReplacesListener(t *testing.T) {
	first, second := &fakeListener{}, &fakeListener{}

	Set(first)
	Set(second)
	Notify()

	if first.calls != 0 {
		t.Fatalf("replaced listener fired %d times", first.calls)
	}
	if second.calls != 1 {
		t.Fatalf("new listener fired %d times, want 1", second.calls)
	}
}
