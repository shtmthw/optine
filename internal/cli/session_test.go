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
	})
	saved.BeginRun()
	saved.AppendUser("earlier question")
	*saved.Ollama() = append(*saved.Ollama(),
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
