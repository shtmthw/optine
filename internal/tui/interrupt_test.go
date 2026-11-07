package tui

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattthew/optine/internal/harnessSession"
)

var escKey = tea.KeyMsg{Type: tea.KeyEsc}

// startRun submits a message and returns the model plus the context the
// agent received, without running the agent command itself.
func startRun(t *testing.T, approvalCh chan string) (Model, context.Context) {
	t.Helper()

	seen := make(chan context.Context, 1)
	agent := func(c context.Context, _ string, _ string, _ string, _ *bufio.Reader) (AgentResult, error) {
		seen <- c
		return AgentResult{}, nil
	}

	m := New(context.Background(), "Ollama", "m", "/w", nil, agent,
		bufio.NewReader(strings.NewReader("")), approvalCh)
	m, _ = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = typeKey(m, "do work")
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("submit should return a command")
	}

	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("submit should batch the agent and the spinner")
	}
	for _, sub := range batch {
		if sub == nil {
			continue
		}
		// The agent command returns agentFinishedMsg; the tick sleeps.
		// Only the agent matters here, so run each in the background.
		go sub()
	}

	select {
	case c := <-seen:
		return m, c
	case <-time.After(time.Second):
		t.Fatal("agent was never invoked")
	}
	return m, nil
}

// The first Esc only arms the interrupt and says so on screen; the run keeps going.
func TestFirstEscArmsInterruptAndShowsHint(t *testing.T) {
	m, runCtx := startRun(t, make(chan string, 1))

	if strings.Contains(plainText(m.View()), escHint) {
		t.Fatal("hint must not show before Esc is pressed")
	}

	m, cmd := update(m, escKey)
	if !m.escArmed || m.interrupting {
		t.Fatalf("first esc: armed=%v interrupting=%v", m.escArmed, m.interrupting)
	}
	if cmd == nil {
		t.Fatal("first esc should schedule the hint's expiry")
	}
	if runCtx.Err() != nil {
		t.Fatal("first esc must not cancel the run")
	}
	if !strings.Contains(plainText(m.View()), escHint) {
		t.Fatalf("hint %q missing after first esc:\n%s", escHint, plainText(m.View()))
	}
}

// The second Esc cancels the run's context and switches to the interrupting status.
func TestSecondEscCancelsRun(t *testing.T) {
	m, runCtx := startRun(t, make(chan string, 1))

	m, _ = update(m, escKey)
	m, _ = update(m, escKey)

	if !m.interrupting || m.escArmed {
		t.Fatalf("second esc: armed=%v interrupting=%v", m.escArmed, m.interrupting)
	}
	if runCtx.Err() != context.Canceled {
		t.Fatalf("run context err = %v, want Canceled", runCtx.Err())
	}
	view := plainText(m.View())
	if !strings.Contains(view, interruptingText) || strings.Contains(view, escHint) {
		t.Fatalf("interrupting status not shown:\n%s", view)
	}

	// Further presses while winding down are ignored.
	m, cmd := update(m, escKey)
	if cmd != nil || !m.interrupting {
		t.Fatal("esc while interrupting should be a no-op")
	}
}

// An expired arming does not let a later single Esc interrupt.
func TestEscHintExpires(t *testing.T) {
	m, runCtx := startRun(t, make(chan string, 1))

	m, _ = update(m, escKey)
	m, _ = update(m, escExpiredMsg{seq: m.escSeq})
	if m.escArmed {
		t.Fatal("expiry should disarm the first esc")
	}

	m, _ = update(m, escKey)
	if !m.escArmed || runCtx.Err() != nil {
		t.Fatal("an esc after expiry should arm again, not interrupt")
	}

	// A stale expiry from the earlier arming must not disarm the new one.
	m, _ = update(m, escExpiredMsg{seq: m.escSeq - 1})
	if !m.escArmed {
		t.Fatal("stale expiry disarmed the current esc")
	}
}

// Esc does nothing while idle.
func TestEscIdleIsNoop(t *testing.T) {
	m, cmd := update(newTestModel(), escKey)
	if m.escArmed || m.interrupting || cmd != nil {
		t.Fatal("esc while idle should do nothing")
	}
}

// Interrupting with an approval prompt up denies it, so the harness unblocks
// and the tool never runs.
func TestInterruptDeniesPendingApproval(t *testing.T) {
	ch := make(chan string, 1)
	m, runCtx := startRun(t, ch)

	m, _ = update(m, approvalRequestedMsg{})
	m, _ = update(m, escKey)
	m, cmd := update(m, escKey)

	if m.pendingApproval {
		t.Fatal("interrupt should clear the approval prompt")
	}
	if runCtx.Err() == nil {
		t.Fatal("run not cancelled")
	}
	if cmd == nil {
		t.Fatal("interrupt should send the denial")
	}
	cmd()

	select {
	case got := <-ch:
		if got != "n" {
			t.Fatalf("approval answer = %q, want n", got)
		}
	case <-time.After(time.Second):
		t.Fatal("pending approval was never denied")
	}
}

// A prompt that arrives after the interrupt is denied without being shown.
func TestApprovalAfterInterruptIsAutoDenied(t *testing.T) {
	ch := make(chan string, 1)
	m, _ := startRun(t, ch)

	m, _ = update(m, escKey)
	m, _ = update(m, escKey)
	m, cmd := update(m, approvalRequestedMsg{})

	if m.pendingApproval {
		t.Fatal("a prompt during interrupt must not be shown")
	}
	if cmd == nil {
		t.Fatal("a prompt during interrupt should be denied")
	}
	cmd()

	select {
	case got := <-ch:
		if got != "n" {
			t.Fatalf("approval answer = %q, want n", got)
		}
	case <-time.After(time.Second):
		t.Fatal("late approval prompt was never denied")
	}
}

// The interrupted run's result is shown as the interrupt marker, not an
// error, and the UI is ready for the next message.
func TestInterruptedRunShowsMarkerNotError(t *testing.T) {
	m, _ := startRun(t, make(chan string, 1))
	m, _ = update(m, escKey)
	m, _ = update(m, escKey)

	// The harness returns an error wrapping context.Canceled.
	m, _ = update(m, agentFinishedMsg{err: fmt.Errorf("run interrupted by user: %w", context.Canceled)})

	if m.busy || m.interrupting || m.escArmed || m.runCancel != nil {
		t.Fatalf("state not reset: busy=%v interrupting=%v armed=%v cancel=%v",
			m.busy, m.interrupting, m.escArmed, m.runCancel != nil)
	}

	last := m.messages[len(m.messages)-1]
	if last.Role != roleEvent || last.Content != harnessSession.InterruptedNotice {
		t.Fatalf("last message = %+v, want interrupt marker event", last)
	}
	for _, msg := range m.messages {
		if msg.Role == roleError {
			t.Fatalf("interrupt surfaced as an error: %+v", msg)
		}
	}

	// A new message can be submitted right away.
	m = typeKey(m, "next")
	m, cmd := update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.busy || cmd == nil {
		t.Fatal("could not start a new run after the interrupt")
	}
}

// The live interrupt marker renders the same as one recovered on /resume.
func TestInterruptMarkerMatchesResumedTranscript(t *testing.T) {
	sess := &harnessSession.Session{}
	sess.AppendUser("do work")
	sess.AppendInterrupted()

	resumed := TranscriptMessages(sess.Transcript())
	if len(resumed) != 2 {
		t.Fatalf("resumed transcript = %+v", resumed)
	}
	if resumed[0].Role != roleUser {
		t.Fatalf("user turn lost: %+v", resumed[0])
	}
	if resumed[1].Role != roleEvent || resumed[1].Content != harnessSession.InterruptedNotice {
		t.Fatalf("marker should resume as an event, got %+v", resumed[1])
	}
}
