package cli

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattthew/optine/internal/dataTypes"
	"github.com/mattthew/optine/internal/harnessPermissions"
	"github.com/mattthew/optine/internal/harnessSession"
)

// freshAuth points auth.json at a brand-new temp file for one test, so each
// test starts from an empty config and never reads or writes the real
// ~/.config. The previous path is restored on cleanup.
func freshAuth(t *testing.T) {
	t.Helper()

	old := harnessPermissions.AuthPath
	harnessPermissions.AuthPath = filepath.Join(t.TempDir(), "auth.json")
	t.Cleanup(func() { harnessPermissions.AuthPath = old })
}

func reader(s string) *bufio.Reader { return bufio.NewReader(strings.NewReader(s)) }

// saveConnection merges the new key into auth.json without clobbering existing
// connections, and a second call updates the same provider in place.
func TestSaveConnectionMergeAndUpdate(t *testing.T) {
	freshAuth(t)

	if err := saveConnection("Tavily", "key-one"); err != nil {
		t.Fatalf("saveConnection: %v", err)
	}
	if err := saveConnection("OpenAI", "sk-openai"); err != nil {
		t.Fatalf("saveConnection: %v", err)
	}
	if err := saveConnection("tavily", "key-two"); err != nil { // case + update
		t.Fatalf("saveConnection update: %v", err)
	}

	cfg := readAuth(t)
	if len(cfg.Connections) != 2 {
		t.Fatalf("want 2 connections, got %d", len(cfg.Connections))
	}
	if cfg.Connections["tavily"].APIKey != "key-two" {
		t.Fatalf("tavily key = %q, want key-two", cfg.Connections["tavily"].APIKey)
	}
	if cfg.Connections["openai"].APIKey != "sk-openai" {
		t.Fatalf("openai key = %q", cfg.Connections["openai"].APIKey)
	}
}

// saveConnection normalizes the provider name and requires both fields.
func TestSaveConnectionValidation(t *testing.T) {
	freshAuth(t)

	if err := saveConnection("", "key"); err == nil {
		t.Fatal("empty provider should error")
	}
	if err := saveConnection("Tavily", "  "); err == nil {
		t.Fatal("empty key should error")
	}
}

// The auth file is written with owner-only permissions.
func TestSaveConnectionFilePerms(t *testing.T) {
	freshAuth(t)

	if err := saveConnection("Anthropic", "sk-ant"); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(harnessPermissions.AuthPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("auth.json mode = %o, want 600", fi.Mode().Perm())
	}

	// And the stored value round-trips through GetAPIKey's reader.
	if got := readAuth(t).Connections["anthropic"].APIKey; got != "sk-ant" {
		t.Fatalf("round-trip key = %q", got)
	}
}

// RunCommand: quit commands return true, everything else false, and EOF exits.
func TestRunCommandQuitAndContinue(t *testing.T) {
	cases := []struct {
		input string
		quit  bool
	}{
		{"/quit", true},
		{"/exit", true},
		{"/q", true},
		{"/help", false},
		{"/h", false},
		{"/funfact", false},
		{"/unknown", false},
		{"not a command", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := RunCommand(tc.input, true, reader("")); got != tc.quit {
			t.Fatalf("RunCommand(%q) = %v, want %v", tc.input, got, tc.quit)
		}
	}
}

// EOF on the reader means the process should exit cleanly.
func TestRunCommandEOFExits(t *testing.T) {
	if !RunCommand("", false, reader("")) {
		t.Fatal("EOF should return true")
	}
}

// /local and /connect are refused while already inside the agent interface.
func TestRunCommandNestedSessionCommandsRefused(t *testing.T) {
	for _, cmd := range []string{"/local", "/connect"} {
		if RunCommand(cmd, true, reader("")) {
			t.Fatalf("%s inside the interface should not quit", cmd)
		}
	}
}

// selectOption accepts a valid index and rejects out-of-range input until the
// attempt budget runs out.
func TestSelectOption(t *testing.T) {
	data := []string{"a", "b", "c"}

	got, err := selectOption(reader("2\n"), 5, data)
	if err != nil || got != "b" {
		t.Fatalf("selectOption = %q, %v", got, err)
	}

	if _, err := selectOption(reader("9\n0\nx\nnope\nq\n"), 5, data); err == nil {
		t.Fatal("all-invalid input should exhaust the budget and error")
	}

	// A later valid choice within the budget wins.
	got, err = selectOption(reader("9\n3\n"), 5, data)
	if err != nil || got != "c" {
		t.Fatalf("recovery select = %q, %v", got, err)
	}
}

func readAuth(t *testing.T) dataTypes.AuthConfig {
	t.Helper()
	data, err := os.ReadFile(harnessPermissions.AuthPath)
	if err != nil {
		t.Fatalf("reading auth.json: %v", err)
	}
	var cfg dataTypes.AuthConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parsing auth.json: %v", err)
	}
	return cfg
}

// resumeSession returns the loaded session with a replayable transcript so the
// TUI can show the previous conversation.
func TestResumeSessionReturnsSavedTranscript(t *testing.T) {
	freshAuth(t)

	// A TUI resume restores the saved backend; it must not probe git.
	t.Setenv("PATH", "")

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	saved := harnessSession.New(harnessSession.Config{
		Provider:     "Ollama",
		Model:        "test-model",
		Directory:    cwd,
		Kind:         harnessSession.OllamaNative,
		ContextLimit: 4096,
		MaxTurns:     55,
		GitStatus:    true,
	})
	saved.BeginRun()
	saved.AppendUser("earlier question")
	*saved.Ollama() = append(*saved.Ollama(),
		&dataTypes.NativeTooltypeMessage{Role: "assistant", Content: "earlier answer"},
	)

	if err := harnessSession.Commit(saved); err != nil {
		t.Fatalf("commit saved session: %v", err)
	}

	current := harnessSession.New(harnessSession.Config{
		Provider:     "Ollama",
		Model:        "test-model",
		Directory:    cwd,
		Kind:         harnessSession.OllamaNative,
		ContextLimit: 4096,
		MaxTurns:     55,
	})

	next, lines := resumeSession(current, "last")
	if next == nil {
		t.Fatalf("resume returned no session, lines: %v", lines)
	}

	if !next.GitStatus {
		t.Fatal("git status lost while resuming")
	}

	entries := next.Transcript()
	if len(entries) != 2 || entries[0].Content != "earlier question" || entries[1].Content != "earlier answer" {
		t.Fatalf("unexpected transcript: %+v", entries)
	}

	if len(lines) == 0 || !strings.Contains(lines[0], "resumed session") {
		t.Fatalf("resume status line missing: %v", lines)
	}
}
