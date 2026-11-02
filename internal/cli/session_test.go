package cli

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mattthew/optine/internal/dataTypes"
	"github.com/mattthew/optine/internal/harnessSession"
)

func commitTestSession(t *testing.T, gitStatus bool, directory string) *harnessSession.Session {
	t.Helper()

	saved := harnessSession.New(harnessSession.Config{
		Provider:     "Ollama",
		Model:        "test-model",
		Directory:    directory,
		Kind:         harnessSession.OllamaNative,
		ContextLimit: 4096,
		MaxTurns:     55,
		GitStatus:    gitStatus,
		ToolCall:     true,
	})
	saved.BeginRun()
	saved.AppendUser("earlier question")
	*saved.OllamaNative() = append(*saved.OllamaNative(),
		&dataTypes.NativeTooltypeMessage{Role: "assistant", Content: "earlier answer"},
	)

	if err := harnessSession.Commit(saved); err != nil {
		t.Fatalf("commit saved session: %v", err)
	}

	return saved
}

func TestSessionForRunPreservesSavedGitStatus(t *testing.T) {
	for _, wantGit := range []bool{true, false} {
		t.Run(fmt.Sprintf("git=%t", wantGit), func(t *testing.T) {
			freshAuth(t)

			cwd, err := os.Getwd()
			if err != nil {
				t.Fatalf("getwd: %v", err)
			}

			saved := commitTestSession(t, wantGit, cwd)

			oldGitAvailable := gitAvailable
			gitAvailable = func() bool {
				t.Fatal("git availability checked while resuming a session")
				return false
			}
			t.Cleanup(func() { gitAvailable = oldGitAvailable })

			loaded, err := sessionForRun(harnessSession.Config{
				Provider:     "Ollama",
				Model:        "test-model",
				Directory:    cwd,
				Kind:         harnessSession.OllamaNative,
				ContextLimit: 4096,
				MaxTurns:     55,
				GitStatus:    !wantGit,
			}, saved.UID)
			if err != nil {
				t.Fatalf("sessionForRun: %v", err)
			}

			if loaded.GitStatus != wantGit {
				t.Fatalf("GitStatus = %v, want saved value %v", loaded.GitStatus, wantGit)
			}
		})
	}
}

func TestSessionForRunProbesGitForNewSessions(t *testing.T) {
	oldGitAvailable := gitAvailable
	calls := 0
	gitAvailable = func() bool {
		calls++
		return true
	}
	t.Cleanup(func() { gitAvailable = oldGitAvailable })

	sess, err := sessionForRun(harnessSession.Config{
		Provider:  "Ollama",
		Model:     "test-model",
		Directory: t.TempDir(),
		Kind:      harnessSession.OllamaNative,
	}, "")
	if err != nil {
		t.Fatalf("sessionForRun: %v", err)
	}

	if calls != 1 {
		t.Fatalf("git availability checked %d times, want 1", calls)
	}
	if !sess.GitStatus {
		t.Fatal("new session did not adopt the probed git status")
	}
}

func TestSessionForRunRejectsDifferentDirectory(t *testing.T) {
	freshAuth(t)

	saved := commitTestSession(t, false, t.TempDir())

	_, err := sessionForRun(harnessSession.Config{
		Provider:  "Ollama",
		Model:     "test-model",
		Directory: t.TempDir(),
		Kind:      harnessSession.OllamaNative,
	}, saved.UID)
	if err == nil {
		t.Fatal("resuming from a different directory should fail")
	}
	if !strings.Contains(err.Error(), "resume it from that directory") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSessionForRunAlignsKindToCfgProvider ensures a resumed session whose
// saved kind/provider pair is decoupled from the active config gets realigned
// so the looper kind guard passes.
func TestSessionForRunAlignsKindToCfgProvider(t *testing.T) {
	freshAuth(t)

	cwd := t.TempDir()
	saved := commitTestSession(t, false, cwd)

	// The saved session is Ollama; run it under the Openrouter config.
	loaded, err := sessionForRun(harnessSession.Config{
		Provider:     "Openrouter",
		Model:        "anthropic/claude-3",
		Directory:    cwd,
		Kind:         harnessSession.OllamaNative,
		ContextLimit: 4096,
		MaxTurns:     55,
		GitStatus:    saved.GitStatus,
		LocalOnly:    saved.LocalOnly,
		ToolCall:     saved.ToolCall,
	}, saved.UID)
	if err != nil {
		t.Fatalf("sessionForRun: %v", err)
	}

	if loaded.Provider != "Openrouter" {
		t.Fatalf("provider = %q, want %q", loaded.Provider, "Openrouter")
	}
	if loaded.Kind != harnessSession.OpenAICompatNative {
		t.Fatalf("kind = %q, want %q", loaded.Kind, harnessSession.OpenAICompatNative)
	}

	// The system prompt must be rebuilt for the reconciled protocol.
	if strings.TrimSpace(loaded.SystemPrompt) == "" {
		t.Fatal("system prompt not rebuilt on reconcile")
	}
}
