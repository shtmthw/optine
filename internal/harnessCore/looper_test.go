package harnessCore

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattthew/optine/internal/dataTypes"
	"github.com/mattthew/optine/internal/harnessPermissions"
	"github.com/mattthew/optine/internal/harnessSession"
)

// A session that chose git snapshots must not silently fall back to the
// content-addressed backend when git disappears later.
func TestAgentLoopKeepsGitBackendWhenGitIsUnavailable(t *testing.T) {
	oldAuthPath := harnessPermissions.AuthPath
	harnessPermissions.AuthPath = filepath.Join(t.TempDir(), "auth.json")
	t.Cleanup(func() { harnessPermissions.AuthPath = oldAuthPath })

	t.Setenv("PATH", "")

	sess := harnessSession.New(harnessSession.Config{
		Provider:  "Ollama",
		Model:     "test-model",
		Directory: t.TempDir(),
		Kind:      harnessSession.OllamaNative,
		MaxTurns:  MaxTurns,
		GitStatus: true,
	})

	_, err := AgentLoop(
		context.Background(),
		sess,
		dataTypes.AgentConfig{Provider: "Ollama", Model: "test-model", NativeToolCalling: true},
		"hello",
		bufio.NewReader(strings.NewReader("")),
	)
	if err == nil {
		t.Fatal("AgentLoop should fail when the saved git backend cannot run")
	}
	if !sess.GitStatus {
		t.Fatal("AgentLoop changed the session's snapshot backend")
	}

	configDir := filepath.Dir(harnessPermissions.AuthPath)
	if _, statErr := os.Stat(filepath.Join(configDir, "nonGitBasedSnapshots")); !os.IsNotExist(statErr) {
		t.Fatalf("non-git snapshots were used despite the saved git backend, stat err=%v", statErr)
	}
}
