package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mattthew/optine/internal/harnessSession"
)

// Saved transcripts are shown as user prompts plus final answers.
func TestTranscriptMessagesMapsConversation(t *testing.T) {
	entries := []harnessSession.TranscriptEntry{
		{Role: harnessSession.TranscriptUser, Content: "hello"},
		{Role: harnessSession.TranscriptAssistant, Content: "answer"},
	}

	messages := TranscriptMessages(entries)

	if len(messages) != 2 {
		t.Fatalf("got %d messages, want 2: %+v", len(messages), messages)
	}
	if messages[0].Role != roleUser || messages[0].Content != "hello" {
		t.Fatalf("first message wrong: %+v", messages[0])
	}
	if messages[1].Role != roleAgent || messages[1].Content != "answer" {
		t.Fatalf("second message wrong: %+v", messages[1])
	}
}

// The display keeps only the newest messages when a transcript is huge.
func TestTranscriptMessagesCapsHistory(t *testing.T) {
	entries := make([]harnessSession.TranscriptEntry, 0, maxResumeMessages+50)

	for i := 0; i < maxResumeMessages+50; i++ {
		entries = append(entries, harnessSession.TranscriptEntry{
			Role:    harnessSession.TranscriptUser,
			Content: fmt.Sprintf("m%d", i),
		})
	}

	messages := TranscriptMessages(entries)

	if len(messages) != maxResumeMessages {
		t.Fatalf("got %d messages, want %d", len(messages), maxResumeMessages)
	}
	if messages[0].Content != "m50" {
		t.Fatalf("oldest kept message = %q, want m50", messages[0].Content)
	}
	if last := messages[len(messages)-1].Content; last != fmt.Sprintf("m%d", maxResumeMessages+49) {
		t.Fatalf("newest message = %q", last)
	}
}

// The byte cap drops older entries but always keeps the newest one.
func TestTranscriptMessagesCapsBytes(t *testing.T) {
	blob := strings.Repeat("x", maxResumeBytes)

	entries := []harnessSession.TranscriptEntry{
		{Role: harnessSession.TranscriptUser, Content: blob},
		{Role: harnessSession.TranscriptAssistant, Content: blob},
		{Role: harnessSession.TranscriptUser, Content: "tail"},
	}

	messages := TranscriptMessages(entries)

	if len(messages) != 1 || messages[0].Content != "tail" {
		t.Fatalf("got %+v, want only the newest message", messages)
	}
}

// A swap replaces the whole visible conversation instead of appending.
func TestAgentFinishedSwapReplacesConversation(t *testing.T) {
	m := New(
		t.Context(),
		"Ollama",
		"m",
		"/w",
		[]Message{{Role: roleUser, Content: "old"}},
		nil,
		nil,
		nil,
	)
	m.scroll = 4

	m, _ = update(m, agentFinishedMsg{swap: []Message{{Role: roleUser, Content: "new"}}})

	if len(m.messages) != 1 || m.messages[0].Content != "new" {
		t.Fatalf("conversation not replaced: %+v", m.messages)
	}
	if m.scroll != 0 {
		t.Fatalf("scroll = %d, want 0", m.scroll)
	}
}

// An empty non-nil swap clears the screen, e.g. resuming an empty session.
func TestAgentFinishedEmptySwapClearsConversation(t *testing.T) {
	m := New(
		t.Context(),
		"Ollama",
		"m",
		"/w",
		[]Message{{Role: roleUser, Content: "old"}},
		nil,
		nil,
		nil,
	)

	m, _ = update(m, agentFinishedMsg{swap: []Message{}})

	if len(m.messages) != 0 {
		t.Fatalf("conversation not cleared: %+v", m.messages)
	}
}

// Startup resume seeds the visible conversation.
func TestNewSeedsResumeMessages(t *testing.T) {
	resume := []Message{
		{Role: roleUser, Content: "hi"},
		{Role: roleAgent, Content: "hello"},
	}

	m := New(t.Context(), "Ollama", "m", "/w", resume, nil, nil, nil)

	if len(m.messages) != 2 || m.messages[0].Content != "hi" || m.messages[1].Content != "hello" {
		t.Fatalf("resume not seeded: %+v", m.messages)
	}

	resume[0].Content = "mutated"
	if m.messages[0].Content != "hi" {
		t.Fatal("resume slice should be copied")
	}
}
