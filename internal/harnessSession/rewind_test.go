package harnessSession

import (
	"slices"
	"testing"
)

// rewindHistory is two runs of the envelope protocol plus an interrupted run:
// every kind of user-role plumbing sits between the real user messages.
func rewindHistory() []Message {
	return []Message{
		{Role: "user", Content: "first"}, // 0: turn 1
		{Role: "assistant", Content: `{"type":"tool_call","tool":"bash","arguments":{}}`}, // 1
		{Role: "user", Content: "TOOL_RESULT\ntool: bash\n\nresult:\nok"},                 // 2: tool result
		{Role: "user", Content: envelopeRetryPrefix + " try again"},                       // 3: retry notice
		{Role: "assistant", Content: `{"type":"final_answer","content":"done"}`},          // 4
		{Role: "user", Content: "second"},                                                 // 5: turn 2
		{Role: "user", Content: InterruptedNotice},                                        // 6: interrupt marker
		{Role: "user", Content: "third"},                                                  // 7: turn 3
		{Role: "assistant", Content: `{"type":"final_answer","content":"bye"}`},           // 8
	}
}

func TestUserTurnsSkipsPlumbing(t *testing.T) {
	sess := &Session{Messages: rewindHistory(), TotalUserMessages: 3}

	want := []UserTurn{
		{Number: 1, Index: 0, Content: "first"},
		{Number: 2, Index: 5, Content: "second"},
		{Number: 3, Index: 7, Content: "third"},
	}
	if got := sess.UserTurns(); !slices.Equal(got, want) {
		t.Fatalf("UserTurns = %+v, want %+v", got, want)
	}

	if (*Session)(nil).UserTurns() != nil {
		t.Fatal("a nil session has no turns")
	}
}

// RewindTo keeps everything before the turn and resets the user message count
// that numbers the next run's snapshots.
func TestRewindToCutsHistoryAndCount(t *testing.T) {
	sess := &Session{Messages: rewindHistory(), TotalUserMessages: 3}

	sess.RewindTo(sess.UserTurns()[1])

	if !slices.EqualFunc(sess.Messages, rewindHistory()[:5], messagesEqual) {
		t.Fatalf("messages = %+v, want the first run only", sess.Messages)
	}
	if sess.TotalUserMessages != 1 {
		t.Fatalf("TotalUserMessages = %d, want 1", sess.TotalUserMessages)
	}

	sess.RewindTo(sess.UserTurns()[0])
	if len(sess.Messages) != 0 || sess.TotalUserMessages != 0 {
		t.Fatalf("rewinding the first message left %d messages, count %d", len(sess.Messages), sess.TotalUserMessages)
	}
}

// TranscriptBefore shows the conversation as it reads once rewound to index,
// without touching the history itself.
func TestTranscriptBeforeLeavesHistoryAlone(t *testing.T) {
	sess := &Session{Messages: rewindHistory()}

	got := sess.TranscriptBefore(5)
	want := []TranscriptEntry{
		{Role: TranscriptUser, Content: "first"},
		{Role: TranscriptToolCall, Content: "tool call: bash"},
		{Role: TranscriptToolResult, Content: "ok"},
		{Role: TranscriptAssistant, Content: "done"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("TranscriptBefore(5) = %+v, want %+v", got, want)
	}

	if len(sess.Messages) != len(rewindHistory()) {
		t.Fatal("TranscriptBefore changed the history")
	}
	if len(sess.TranscriptBefore(-1)) != 0 {
		t.Fatal("an index before the start shows nothing")
	}
	if !slices.Equal(sess.TranscriptBefore(99), sess.Transcript()) {
		t.Fatal("an index past the end shows the whole transcript")
	}
}

func messagesEqual(a, b Message) bool {
	return a.Role == b.Role && a.Content == b.Content && a.ToolCallID == b.ToolCallID && len(a.ToolCalls) == len(b.ToolCalls)
}
