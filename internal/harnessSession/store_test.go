package harnessSession

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	dir := stubSessionsDir(t)

	memory := "notebook"
	crawlers := []string{"searxng"}
	stubSystemInputs(t, "/tmp/memory.md", &memory, &crawlers)

	sess := New(Config{Provider: "Ollama", Model: "m", Directory: "/home/matthew/optine", Kind: OllamaNative, GitStatus: true, ToolCall: true})
	sess.BeginRun()
	sess.AppendUser("hello")

	sess.Messages = append(sess.Messages,
		Message{
			Role:      "assistant",
			ToolCalls: []ToolCall{{ID: "call_1", Name: "bash", Arguments: map[string]any{"command": "ls"}}},
		},
		Message{Role: "tool", ToolCallID: "call_1", Content: "result"},
		Message{Role: "assistant", Content: "done"},
	)
	sess.RecordPromptTokens(42)

	if err := Commit(sess); err != nil {
		t.Fatalf("commit: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, sess.UID+".json"))
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}
	if !strings.Contains(string(raw), `"total_user_messages": 1`) {
		t.Fatalf("session file missing total_user_messages: %s", raw)
	}
	if !strings.Contains(string(raw), `"tool_call": true`) {
		t.Fatalf("session file missing tool_call: %s", raw)
	}
	if !strings.Contains(string(raw), `"messages":`) {
		t.Fatalf("session file missing unified messages array: %s", raw)
	}
	for _, legacy := range []string{`"ollama-native":`, `"openai-chat-completions":`, `"envelope":`} {
		if strings.Contains(string(raw), legacy) {
			t.Fatalf("session file still contains legacy key %s: %s", legacy, raw)
		}
	}

	metas, err := List("", false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(metas) != 1 || metas[0].UID != sess.UID || metas[0].Messages != sess.Len() || metas[0].Directory != sess.Directory {
		t.Fatalf("unexpected listing: %+v", metas)
	}
	if !metas[0].GitStatus {
		t.Fatal("git status lost in listing")
	}
	if !metas[0].ToolCall {
		t.Fatal("tool call lost in listing")
	}
	if metas[0].TotalUserMessages != 1 {
		t.Fatalf("user message count lost in listing: %d", metas[0].TotalUserMessages)
	}

	loaded, err := Load(sess.UID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if loaded.Kind != sess.Kind || loaded.Provider != sess.Provider || loaded.Model != sess.Model || loaded.Directory != sess.Directory {
		t.Fatalf("metadata lost on load: %+v", loaded)
	}
	if !loaded.GitStatus {
		t.Fatal("git status lost on load")
	}
	if !loaded.ToolCall {
		t.Fatal("tool call lost on load")
	}
	if loaded.TotalUserMessages != 1 {
		t.Fatalf("user message count lost on load: %d", loaded.TotalUserMessages)
	}

	if loaded.Len() != sess.Len() {
		t.Fatalf("history length: want %d, got %d", sess.Len(), loaded.Len())
	}

	if loaded.LastPromptTokens != 42 || loaded.RunCount != 1 {
		t.Fatalf("telemetry lost: tokens=%d runs=%d", loaded.LastPromptTokens, loaded.RunCount)
	}

	msgs := loaded.Messages
	if msgs[3].Content != "done" {
		t.Fatalf("history order lost: %q", msgs[3].Content)
	}

	if !strings.Contains(loaded.SystemPrompt, "notebook") {
		t.Fatalf("system prompt was not refreshed from the notebook: %q", loaded.SystemPrompt)
	}
	for i, msg := range msgs {
		if msg.Role == "system" {
			t.Fatalf("system prompt must not live in the message array (index %d)", i)
		}
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
		Kind:     OpenAICompatNative,
		Provider: "vLLM",
		Model:    "m",
		RunCount: 1,
		Messages: []Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "bash"}}},
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

func TestPairValidationAcceptsCompletePair(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "bash"}}},
		{Role: "tool", ToolCallID: "call_1", Content: "ok"},
	}

	if err := validateToolPairs(msgs); err != nil {
		t.Fatalf("complete pair should validate: %v", err)
	}

	// Ollama may omit call ids; the count is enough in that case.
	msgs = []Message{
		{Role: "assistant", ToolCalls: []ToolCall{{Name: "bash"}}},
		{Role: "tool", Content: "ok"},
	}
	if err := validateToolPairs(msgs); err != nil {
		t.Fatalf("count-only pair should validate: %v", err)
	}
}

func TestLoadReconcilesKindProviderMismatch(t *testing.T) {
	dir := stubSessionsDir(t)

	memory := "notebook"
	crawlers := []string{"searxng"}
	stubSystemInputs(t, "/tmp/memory.md", &memory, &crawlers)

	broken := sessionFile{
		UID:      "s2",
		Kind:     OllamaNative,
		Provider: "Openrouter",
		Model:    "m",
		RunCount: 1,
		Messages: []Message{
			{Role: "user", Content: "hi"},
		},
	}

	data, err := json.Marshal(broken)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s2.json"), data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	loaded, err := Load("s2")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Kind != OpenAICompatNative {
		t.Fatalf("kind not reconciled: %q", loaded.Kind)
	}
	if loaded.Provider != "Openrouter" {
		t.Fatalf("provider changed on reconcile: %q", loaded.Provider)
	}

	// The prompt is rebuilt for the reconciled protocol and stays top-level.
	if !strings.Contains(loaded.SystemPrompt, "notebook") {
		t.Fatalf("reconciled system prompt lacks notebook content: %q", loaded.SystemPrompt)
	}

	// History is provider-neutral and stays put across the kind change.
	if len(loaded.Messages) != 1 || loaded.Messages[0].Role != "user" || loaded.Messages[0].Content != "hi" {
		t.Fatalf("history not preserved on reconcile: %+v", loaded.Messages)
	}
}
