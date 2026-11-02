package harnessSession

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

// stubSystemInputs replaces the notebook/crawler readers so tests never touch
// the real config dir. Returned setters let a test mutate the inputs.
func stubSystemInputs(t *testing.T, path string, memory *string, crawlers *[]string) {
	t.Helper()

	oldPath, oldRead, oldCrawlers := casualMemoryPath, readCasualMemoryFile, availableCrawlers

	casualMemoryPath = func() (string, error) { return path, nil }
	readCasualMemoryFile = func() (string, error) { return *memory, nil }
	availableCrawlers = func() []string { return *crawlers }

	t.Cleanup(func() {
		casualMemoryPath, readCasualMemoryFile, availableCrawlers = oldPath, oldRead, oldCrawlers
	})
}

func TestNewUIDFormat(t *testing.T) {
	pattern := regexp.MustCompile(`^\d{8}T\d{6}Z-[0-9a-f]{8}$`)

	if uid := NewUID(); !pattern.MatchString(uid) {
		t.Fatalf("uid %q does not match the timestamp-prefixed format", uid)
	}
}

// TestAppendUserWritesSharedHistory proves every append lands in the one
// provider-neutral history, regardless of Kind.
func TestAppendUserWritesSharedHistory(t *testing.T) {
	memory := "notes"
	crawlers := []string{"searxng"}
	stubSystemInputs(t, "/tmp/memory.md", &memory, &crawlers)

	sess := New(Config{Provider: "Ollama", Model: "m", Kind: OllamaNative})
	start := len(sess.Messages)

	for i := 0; i < 500; i++ {
		sess.AppendUser("x")
	}

	if got := sess.Len(); got != start+500 {
		t.Fatalf("want %d messages after appends, got %d", start+500, got)
	}

	first := sess.Messages[start]
	if first.Role != "user" || first.Content != "x" {
		t.Fatalf("unexpected stored message: %+v", first)
	}
}

func TestSyncSystemOnlyRebuildsOnChange(t *testing.T) {
	memory := "first note"
	crawlers := []string{"searxng"}
	stubSystemInputs(t, "/tmp/memory.md", &memory, &crawlers)

	sess := New(Config{Provider: "Ollama", Model: "m", Kind: OllamaNative})

	before := sess.SystemPrompt
	if !strings.Contains(before, "first note") {
		t.Fatalf("system prompt lacks the notebook content: %q", before)
	}

	if sess.SyncSystem() {
		t.Fatal("unchanged memory rebuilt the system prompt")
	}

	if after := sess.SystemPrompt; after != before {
		t.Fatal("system prompt changed even though the memory did not")
	}

	memory = "second note"

	if !sess.SyncSystem() {
		t.Fatal("changed memory did not rebuild the system prompt")
	}

	after := sess.SystemPrompt
	if !strings.Contains(after, "second note") || strings.Contains(after, "first note") {
		t.Fatalf("system prompt was not refreshed: %q", after)
	}
}

func TestSyncSystemKeepsSnapshotOnReadError(t *testing.T) {
	memory := "keep me"
	crawlers := []string{"searxng"}
	stubSystemInputs(t, "/tmp/memory.md", &memory, &crawlers)

	sess := New(Config{Provider: "Ollama", Model: "m", Kind: OllamaNative})

	readCasualMemoryFile = func() (string, error) { return "", errors.New("boom") }

	if sess.SyncSystem() {
		t.Fatal("a read error must not rebuild the prompt")
	}

	if content := sess.SystemPrompt; !strings.Contains(content, "keep me") {
		t.Fatalf("last good memory was lost on read error: %q", content)
	}
}

func TestSyncSystemCrawlerChangeRebuilds(t *testing.T) {
	memory := "notes"
	crawlers := []string{"searxng"}
	stubSystemInputs(t, "/tmp/memory.md", &memory, &crawlers)

	sess := New(Config{Provider: "Ollama", Model: "m", Kind: OllamaNative})
	before := sess.SystemPrompt

	crawlers = []string{"searxng", "tavily"}

	if !sess.SyncSystem() {
		t.Fatal("crawler change did not rebuild the system prompt")
	}

	if after := sess.SystemPrompt; after == before {
		t.Fatal("system prompt stayed identical after a crawler change")
	}
}

func TestSyncSystemRebuildsOnToolCallChange(t *testing.T) {
	memory := "notes"
	crawlers := []string{"searxng"}
	stubSystemInputs(t, "/tmp/memory.md", &memory, &crawlers)

	sess := New(Config{Provider: "Ollama", Model: "m", Kind: OllamaNative, ToolCall: true})
	if !strings.Contains(sess.SystemPrompt, "Tool descriptions say") {
		t.Fatalf("native prompt missing at creation: %q", sess.SystemPrompt)
	}

	sess.ToolCall = false

	if !sess.SyncSystem() {
		t.Fatal("a tool call change must rebuild the system prompt")
	}
	if !strings.Contains(sess.SystemPrompt, "you cannot run them yourself") {
		t.Fatalf("prompt not switched to the envelope protocol: %q", sess.SystemPrompt)
	}
}

func TestReplaceSessionSysPromptSwapsAndPersists(t *testing.T) {
	stubSessionsDir(t)

	memory := "notes"
	crawlers := []string{"searxng"}
	stubSystemInputs(t, "/tmp/memory.md", &memory, &crawlers)

	sess := New(Config{
		Provider:  "Ollama",
		Model:     "m",
		Directory: t.TempDir(),
		Kind:      OllamaNative,
		ToolCall:  true,
	})
	sess.BeginRun()
	sess.AppendUser("hi")

	if err := ReplaceSessionSysPrompt(false, sess); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if sess.Kind != Envelope || sess.ToolCall {
		t.Fatalf("protocol not switched: kind=%q toolCall=%v", sess.Kind, sess.ToolCall)
	}
	if len(sess.Messages) != 1 || sess.Messages[0].Content != "hi" {
		t.Fatalf("protocol switch lost history: %+v", sess.Messages)
	}
	if !strings.Contains(sess.SystemPrompt, "you cannot run them yourself") {
		t.Fatalf("prompt not switched: %q", sess.SystemPrompt)
	}

	loaded, err := Load(sess.UID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Kind != Envelope || loaded.ToolCall {
		t.Fatalf("protocol not persisted: kind=%q toolCall=%v", loaded.Kind, loaded.ToolCall)
	}
	if len(loaded.Messages) != 1 || loaded.Messages[0].Content != "hi" {
		t.Fatalf("resume lost history: %+v", loaded.Messages)
	}
	if !strings.Contains(loaded.SystemPrompt, "you cannot run them yourself") {
		t.Fatalf("swapped prompt not persisted: %q", loaded.SystemPrompt)
	}
}

func TestKindFor(t *testing.T) {
	cases := []struct {
		provider string
		native   bool
		want     Kind
	}{
		{"Ollama", true, OllamaNative},
		{"vLLM", true, OpenAICompatNative},
		{"Openrouter", true, OpenAICompatNative},
		{"Ollama", false, Envelope},
		{"vLLM", false, Envelope},
		{"Openrouter", false, Envelope},
	}

	for _, tc := range cases {
		if got := KindFor(tc.provider, tc.native); got != tc.want {
			t.Errorf("KindFor(%q, %t) = %q, want %q", tc.provider, tc.native, got, tc.want)
		}
	}
}

func TestResumeableAlwaysAllows(t *testing.T) {
	sess := &Session{UID: "u", Kind: OllamaNative}

	if err := sess.Resumeable(OllamaNative); err != nil {
		t.Fatalf("same kind should be resumable: %v", err)
	}

	if err := sess.Resumeable(OpenAICompatNative); err != nil {
		t.Fatal("cross-kind resume must be allowed")
	}

	if err := sess.Resumeable(Envelope); err != nil {
		t.Fatal("envelope resume must be allowed")
	}
}

func TestReconcileKindHealsMismatch(t *testing.T) {
	memory := "notes"
	crawlers := []string{"searxng"}
	stubSystemInputs(t, "/tmp/memory.md", &memory, &crawlers)

	sess := New(Config{Provider: "Openrouter", Model: "m", Kind: OllamaNative})
	if sess.Kind != OllamaNative {
		t.Fatalf("initial kind %q", sess.Kind)
	}
	sess.AppendUser("hi")

	sess.ReconcileKind()

	if sess.Kind != OpenAICompatNative {
		t.Fatalf("reconciled kind %q, want %q", sess.Kind, OpenAICompatNative)
	}
	if !sess.ToolCall {
		t.Fatal("native tool calling lost on reconcile")
	}
	if sess.Provider != "Openrouter" {
		t.Fatalf("provider changed on reconcile: %q", sess.Provider)
	}

	if !strings.Contains(sess.SystemPrompt, "notes") {
		t.Fatalf("reconciled system prompt lacks notebook content: %q", sess.SystemPrompt)
	}
	if len(sess.Messages) != 1 || sess.Messages[0].Content != "hi" {
		t.Fatalf("reconcile lost history: %+v", sess.Messages)
	}
}

func TestReconcileKindConsistentIsNoOp(t *testing.T) {
	sess := &Session{Kind: OllamaNative, Provider: "Ollama"}
	sess.SystemPrompt = "sys"
	sess.ReconcileKind()
	if sess.Kind != OllamaNative {
		t.Fatalf("consistent session should be a no-op, got %q", sess.Kind)
	}
	if !sess.ToolCall {
		t.Fatal("consistent native session should enable tool calling")
	}
	if sess.SystemPrompt != "sys" {
		t.Fatalf("consistent session must not rebuild the prompt, got %q", sess.SystemPrompt)
	}
}

func TestReconcileKindEnvelopeIsNoOp(t *testing.T) {
	sess := &Session{Kind: Envelope, Provider: "Ollama"}
	sess.SystemPrompt = "sys"
	sess.ReconcileKind()
	if sess.Kind != Envelope {
		t.Fatalf("envelope should be a no-op, got %q", sess.Kind)
	}
	if sess.ToolCall {
		t.Fatal("envelope session must not enable tool calling")
	}
	if sess.SystemPrompt != "sys" {
		t.Fatalf("envelope session must not rebuild the prompt, got %q", sess.SystemPrompt)
	}
}
