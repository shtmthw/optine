package harnessSession

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/mattthew/optine/internal/dataTypes"
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

// TestPointerSlotWriteBack proves the slot accessor returns a stable handle:
// appends through it keep landing in the session even after the backing array
// is reallocated.
func TestPointerSlotWriteBack(t *testing.T) {
	memory := "notes"
	crawlers := []string{"searxng"}
	stubSystemInputs(t, "/tmp/memory.md", &memory, &crawlers)

	sess := New(Config{Provider: "Ollama", Model: "m", Kind: OllamaNative})
	slot := sess.Ollama()
	start := len(*slot)

	for i := 0; i < 500; i++ {
		*slot = append(*slot, &dataTypes.NativeTooltypeMessage{Role: "user", Content: "x"})
	}

	if got := sess.Len(); got != start+500 {
		t.Fatalf("want %d messages after pointer appends, got %d", start+500, got)
	}
}

func TestSyncSystemOnlyRebuildsOnChange(t *testing.T) {
	memory := "first note"
	crawlers := []string{"searxng"}
	stubSystemInputs(t, "/tmp/memory.md", &memory, &crawlers)

	sess := New(Config{Provider: "Ollama", Model: "m", Kind: OllamaNative})

	before := (*sess.Ollama())[0].Content
	if !strings.Contains(before, "first note") {
		t.Fatalf("system prompt lacks the notebook content: %q", before)
	}

	if sess.SyncSystem() {
		t.Fatal("unchanged memory rebuilt the system prompt")
	}

	if after := (*sess.Ollama())[0].Content; after != before {
		t.Fatal("system prompt changed even though the memory did not")
	}

	memory = "second note"

	if !sess.SyncSystem() {
		t.Fatal("changed memory did not rebuild the system prompt")
	}

	after := (*sess.Ollama())[0].Content
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

	if content := (*sess.Ollama())[0].Content; !strings.Contains(content, "keep me") {
		t.Fatalf("last good memory was lost on read error: %q", content)
	}
}

func TestSyncSystemCrawlerChangeRebuilds(t *testing.T) {
	memory := "notes"
	crawlers := []string{"searxng"}
	stubSystemInputs(t, "/tmp/memory.md", &memory, &crawlers)

	sess := New(Config{Provider: "Ollama", Model: "m", Kind: OllamaNative})
	before := (*sess.Ollama())[0].Content

	crawlers = []string{"searxng", "tavily"}

	if !sess.SyncSystem() {
		t.Fatal("crawler change did not rebuild the system prompt")
	}

	if after := (*sess.Ollama())[0].Content; after == before {
		t.Fatal("system prompt stayed identical after a crawler change")
	}
}

func TestResumeableKindMismatch(t *testing.T) {
	sess := &Session{UID: "u", Kind: OllamaNative}

	if err := sess.Resumeable(OllamaNative); err != nil {
		t.Fatalf("same kind should be resumable: %v", err)
	}

	if err := sess.Resumeable(VLLMNative); err == nil {
		t.Fatal("cross-kind resume must be rejected")
	}
}
