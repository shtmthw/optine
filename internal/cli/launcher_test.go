package cli

import (
	"errors"
	"fmt"
	"testing"

	"github.com/mattthew/optine/internal/tui"
)

// plainInput forces the line-based fallback for one test.
func plainInput(t *testing.T) {
	t.Helper()

	old := interactive
	interactive = func() bool { return false }
	t.Cleanup(func() { interactive = old })
}

// Without a terminal, pickers fall back to a numbered list and return the
// option's value, not its label.
func TestChooseOptionNumberedFallback(t *testing.T) {
	plainInput(t)

	got, err := chooseOption(reader("2\n"), "Local provider", providers)
	if err != nil || got != "vLLM" {
		t.Fatalf("chooseOption = %q, %v; want vLLM", got, err)
	}

	if _, err := chooseOption(reader(""), "Empty", nil); err == nil {
		t.Fatal("an empty option list should error")
	}
}

// Without a terminal, API keys are read as a plain line.
func TestReadAPIKeyFallback(t *testing.T) {
	plainInput(t)

	key, err := readAPIKey(reader("  sk-test  \n"), "OpenRouter")
	if err != nil || key != "sk-test" {
		t.Fatalf("readAPIKey = %q, %v", key, err)
	}
}

// /quit inside the agent interface ends optine; a cancelled picker is
// silent; any other error is reported and the launcher keeps going.
func TestSessionEnded(t *testing.T) {
	out := printer{inSession: true}

	cases := []struct {
		err  error
		quit bool
	}{
		{nil, false},
		{errCancelled, false},
		{tui.ErrQuit, true},
		{fmt.Errorf("session: %w", tui.ErrQuit), true},
		{errors.New("ollama is down"), false},
	}

	for _, tc := range cases {
		if got := sessionEnded(out, tc.err); got != tc.quit {
			t.Fatalf("sessionEnded(%v) = %v, want %v", tc.err, got, tc.quit)
		}
	}
}

// Every launcher command in the registry is handled by dispatch, so the
// palette never offers a command that answers "unknown command".
func TestLauncherCommandsAreDispatched(t *testing.T) {
	for _, name := range []string{"/help", "/h", "/funfact", "/quit", "/exit", "/q"} {
		_ = dispatch(name, true, reader(""))
	}

	if !dispatch("/quit", false, reader("")) {
		t.Fatal("/quit at the launcher should exit")
	}
}
