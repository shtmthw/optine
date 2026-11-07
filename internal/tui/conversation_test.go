package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mattthew/optine/internal/harnessSession"
	"github.com/mattthew/optine/internal/harnessTools"
)

// Saved transcripts are shown as user prompts, assistant replies and tools.
func TestTranscriptMessagesMapsConversation(t *testing.T) {
	entries := []harnessSession.TranscriptEntry{
		{Role: harnessSession.TranscriptUser, Content: "hello"},
		{Role: harnessSession.TranscriptAssistant, Content: "answer"},
		{Role: harnessSession.TranscriptToolCall, Content: "tool call: bash"},
		{Role: harnessSession.TranscriptToolResult, Content: "tool result"},
	}

	messages := TranscriptMessages(entries)

	if len(messages) != 4 {
		t.Fatalf("got %d messages, want 4: %+v", len(messages), messages)
	}
	if messages[0].Role != roleUser || messages[0].Content != "hello" {
		t.Fatalf("first message wrong: %+v", messages[0])
	}
	if messages[1].Role != roleAgent || messages[1].Content != "answer" {
		t.Fatalf("second message wrong: %+v", messages[1])
	}
	if messages[2].Role != roleToolCall || messages[2].Content != "tool call: bash" {
		t.Fatalf("third message wrong: %+v", messages[2])
	}
	if messages[3].Role != roleToolResult || messages[3].Content != "tool result" {
		t.Fatalf("fourth message wrong: %+v", messages[3])
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

// A single oversized newest message is still shown instead of blanking resume.
func TestTranscriptMessagesKeepsOversizedNewest(t *testing.T) {
	blob := strings.Repeat("x", maxResumeBytes+1)

	messages := TranscriptMessages([]harnessSession.TranscriptEntry{
		{Role: harnessSession.TranscriptUser, Content: blob},
	})

	if len(messages) != 1 || messages[0].Content != blob {
		t.Fatalf("got %d messages, want the oversized newest", len(messages))
	}
}

// Live tool events and resumed tool entries go through the exact same renderer.
func TestLiveToolEventsMatchResume(t *testing.T) {
	edit := harnessTools.FormatEditResult(
		"/work/a.go", "/work", "a.go",
		3, 4, 2, 10, 22,
		"old line\n", "new line\nsecond new line\n",
	)

	entries := []harnessSession.TranscriptEntry{
		{Role: harnessSession.TranscriptUser, Content: "change it"},
		{Role: harnessSession.TranscriptAssistant, Content: "working"},
		{Role: harnessSession.TranscriptToolCall, Content: `tool call: edit_file {"path":"a.go"}`},
		{Role: harnessSession.TranscriptToolResult, Content: edit},
		{Role: harnessSession.TranscriptAssistant, Content: "done"},
	}

	resumed := newTestModelAt(80, 40)
	resumed = modelWithMessages(resumed, TranscriptMessages(entries)...)

	live := newTestModelAt(80, 40)
	live = modelWithMessages(live, Message{Role: roleUser, Content: entries[0].Content})
	live, _ = update(live, transcriptEventMsg{role: roleAgent, content: entries[1].Content})
	live, _ = update(live, transcriptEventMsg{role: roleToolCall, content: entries[2].Content})
	live, _ = update(live, transcriptEventMsg{role: roleToolResult, content: entries[3].Content})
	live, _ = update(live, transcriptEventMsg{role: roleAgent, content: entries[4].Content})

	if got, want := resumed.View(), live.View(); got != want {
		t.Fatalf("live and resumed views differ\n--- resumed ---\n%s\n--- live ---\n%s", escapeANSI(want), escapeANSI(got))
	}
}

func TestResumedNativeSessionRendersTools(t *testing.T) {
	edit := harnessTools.FormatEditResult(
		"/work/a.go", "/work", "a.go",
		3, 4, 2, 10, 22,
		"old line\n", "new line\nsecond new line\n",
	)

	sess := &harnessSession.Session{
		Kind: harnessSession.OllamaNative,
		Messages: []harnessSession.Message{
			{Role: "user", Content: "inspect and fix"},
			{Role: "assistant", ToolCalls: []harnessSession.ToolCall{
				{ID: "a", Name: "read_file", Arguments: map[string]any{"path": "a.go"}},
				{ID: "b", Name: "edit_file", Arguments: map[string]any{"path": "a.go"}},
			}},
			{Role: "tool", ToolCallID: "a", Content: "file contents"},
			{Role: "tool", ToolCallID: "b", Content: edit},
			{Role: "assistant", Content: "fixed"},
		},
	}

	m := newTestModelAt(100, 60)
	m = modelWithMessages(m, TranscriptMessages(sess.Transcript())...)
	view := plainText(m.View())

	for _, want := range []string{
		"read_file",
		"file contents",
		"edit_file",
		"a.go",
		"fixed",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("resumed view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "tool call:") {
		t.Fatalf("raw tool call line leaked into view:\n%s", view)
	}
}

func TestResumedEnvelopeSessionRendersTools(t *testing.T) {
	sess := &harnessSession.Session{
		Kind: harnessSession.Envelope,
		Messages: []harnessSession.Message{
			{Role: "user", Content: "run it"},
			{Role: "assistant", Content: `{"type":"tool_call","tool":"bash","arguments":{"command":"ls"}}`},
			{Role: "user", Content: "TOOL_RESULT\ntool: bash\n\nresult:\nfile.go\n\nUse this result to continue solving the user's request. More instructions."},
			{Role: "assistant", Content: `{"type":"final_answer","content":"done"}`},
		},
	}

	m := newTestModelAt(80, 30)
	m = modelWithMessages(m, TranscriptMessages(sess.Transcript())...)
	view := plainText(m.View())

	for _, want := range []string{"bash", "$ ls", "file.go", "done"} {
		if !strings.Contains(view, want) {
			t.Fatalf("resumed view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Use this result") {
		t.Fatalf("envelope footer leaked into view:\n%s", view)
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
