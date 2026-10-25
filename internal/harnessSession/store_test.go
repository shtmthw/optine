package harnessSession

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattthew/optine/internal/dataTypes"
)

func stubSessionsDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	old := resolveSessionsDir
	resolveSessionsDir = func() (string, error) { return dir, nil }

	t.Cleanup(func() { resolveSessionsDir = old })

	return dir
}

func TestCommitLoadRoundTrip(t *testing.T) {
	stubSessionsDir(t)

	memory := "notebook"
	crawlers := []string{"searxng"}
	stubSystemInputs(t, "/tmp/memory.md", &memory, &crawlers)

	sess := New(Config{Provider: "Ollama", Model: "m", Directory: "/home/matthew/optine", Kind: OllamaNative})
	sess.BeginRun()
	sess.AppendUser("hello")

	*sess.Ollama() = append(*sess.Ollama(),
		&dataTypes.NativeTooltypeMessage{
			Role:      "assistant",
			ToolCalls: []dataTypes.NativeTypeToolCall{{ID: "call_1"}},
		},
		&dataTypes.NativeTooltypeMessage{Role: "tool", ToolCallID: "call_1", Content: "result"},
		&dataTypes.NativeTooltypeMessage{Role: "assistant", Content: "done"},
	)
	sess.RecordPromptTokens(42)

	if err := Commit(sess); err != nil {
		t.Fatalf("commit: %v", err)
	}

	metas, err := List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(metas) != 1 || metas[0].UID != sess.UID || metas[0].Messages != sess.Len() || metas[0].Directory != sess.Directory {
		t.Fatalf("unexpected listing: %+v", metas)
	}

	loaded, err := Load(sess.UID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if loaded.Kind != sess.Kind || loaded.Provider != sess.Provider || loaded.Model != sess.Model || loaded.Directory != sess.Directory {
		t.Fatalf("metadata lost on load: %+v", loaded)
	}

	if loaded.Len() != sess.Len() {
		t.Fatalf("history length: want %d, got %d", sess.Len(), loaded.Len())
	}

	if loaded.LastPromptTokens != 42 || loaded.RunCount != 1 {
		t.Fatalf("telemetry lost: tokens=%d runs=%d", loaded.LastPromptTokens, loaded.RunCount)
	}

	msgs := *loaded.Ollama()
	if msgs[4].Content != "done" {
		t.Fatalf("history order lost: %q", msgs[4].Content)
	}

	if !strings.Contains(msgs[0].Content, "notebook") {
		t.Fatalf("system message was not refreshed from the notebook: %q", msgs[0].Content)
	}
}

func TestCommitSkipsZeroRunSession(t *testing.T) {
	dir := stubSessionsDir(t)

	memory := ""
	crawlers := []string{"searxng"}
	stubSystemInputs(t, "/tmp/memory.md", &memory, &crawlers)

	sess := New(Config{Provider: "Ollama", Model: "m", Kind: OllamaNative})

	if err := Commit(sess); err != nil {
		t.Fatalf("commit: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	if len(entries) != 0 {
		t.Fatalf("a session that never ran must not be persisted, found %d files", len(entries))
	}
}

func TestLoadRejectsOrphanedToolCall(t *testing.T) {
	dir := stubSessionsDir(t)

	broken := sessionFile{
		UID:      "s1",
		Kind:     VLLMNative,
		Provider: "vLLM",
		Model:    "m",
		RunCount: 1,
		VLLM: []*dataTypes.VLLMTooltypeMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hi"},
			{Role: "assistant", ToolCalls: []dataTypes.VLLMTypeToolCall{{ID: "call_1"}}},
		},
	}

	data, err := json.Marshal(broken)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "s1.json"), data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := Load("s1"); err == nil {
		t.Fatal("history with an orphaned tool call must be rejected")
	}
}

func TestLoadRejectsPathTraversal(t *testing.T) {
	stubSessionsDir(t)

	if _, err := Load("../secrets"); err == nil {
		t.Fatal("path traversal in a session id must be rejected")
	}
}

func TestVLLMPairValidationAcceptsCompletePair(t *testing.T) {
	msgs := []*dataTypes.VLLMTooltypeMessage{
		{Role: "system", Content: "sys"},
		{Role: "assistant", ToolCalls: []dataTypes.VLLMTypeToolCall{{ID: "call_1"}}},
		{Role: "tool", ToolCallID: "call_1", Content: "ok"},
	}

	if err := validateVLLMPairs(msgs); err != nil {
		t.Fatalf("complete pair should validate: %v", err)
	}
}
