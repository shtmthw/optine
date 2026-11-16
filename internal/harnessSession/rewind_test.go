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

// compactedSession has five finished turns, with compaction #1 covering turns
// 1-2 and compaction #2 covering turns 1-4.
func compactedSession() *Session {
	sess := &Session{LastPromptTokens: 900}
	for _, text := range []string{"one", "two", "three", "four", "five"} {
		sess.AppendUser(text)
		sess.Messages = append(sess.Messages, Message{Role: "assistant", Content: "done " + text})
	}
	sess.AddCompaction("summary of 1-2", 2)
	sess.AddCompaction("summary of 1-4", 4)

	return sess
}

// A rewind drops the compactions covering the picked turn and keeps the older
// ones, which only summarize turns still in the history. The reconstructed
// session is then rebuilt from the latest one left.
func TestRewindToDropsCompactionsCoveringTheTurn(t *testing.T) {
	cases := []struct {
		turn   int
		kept   []int // compaction numbers left
		reSess []string
	}{
		{turn: 5, kept: []int{1, 2}, reSess: []string{"summary of 1-4"}},
		{turn: 4, kept: []int{1}, reSess: []string{"summary of 1-2", "three", "done three"}},
		{turn: 3, kept: []int{1}, reSess: []string{"summary of 1-2"}},
		{turn: 2, kept: nil},
		{turn: 1, kept: nil},
	}

	for _, tc := range cases {
		sess := compactedSession()
		sess.RewindTo(sess.UserTurns()[tc.turn-1])

		var kept []int
		for _, c := range sess.Compactions {
			kept = append(kept, c.Number)
		}
		if !slices.Equal(kept, tc.kept) {
			t.Fatalf("rewind to %d kept compactions %v, want %v", tc.turn, kept, tc.kept)
		}
		if sess.LastPromptTokens != 0 {
			t.Fatalf("rewind to %d kept the old prompt size %d", tc.turn, sess.LastPromptTokens)
		}

		if tc.kept == nil {
			if sess.IsCompacted() {
				t.Fatalf("rewind to %d left the session compacted", tc.turn)
			}
			if _, err := ReconstructSession(sess); err == nil {
				t.Fatalf("rewind to %d: an uncompacted session was reconstructed", tc.turn)
			}
			continue
		}

		reSess, err := ReconstructSession(sess)
		if err != nil {
			t.Fatalf("rewind to %d: reconstruct: %v", tc.turn, err)
		}
		var got []string
		for _, msg := range reSess.Messages {
			got = append(got, msg.Content)
		}
		if !slices.Equal(got, tc.reSess) {
			t.Fatalf("rewind to %d: reSess = %q, want %q", tc.turn, got, tc.reSess)
		}
	}
}

// Compactions are numbered from 1, oldest first, and a rewind that drops the
// newest ones lets the next compaction take the next free number.
func TestCompactionNumbersContinueAfterARewind(t *testing.T) {
	sess := compactedSession()
	if latest, _ := sess.LatestCompaction(); latest.Number != 2 || latest.ThroughTurn != 4 {
		t.Fatalf("latest = %+v, want #2 through turn 4", latest)
	}

	sess.RewindTo(sess.UserTurns()[3]) // drops #2
	sess.AppendUser("four again")

	if c := sess.AddCompaction("summary of 1-3", 3); c.Number != 2 {
		t.Fatalf("next compaction = %+v, want #2", c)
	}
	if latest, _ := sess.LatestCompaction(); latest.Summary != "summary of 1-3" {
		t.Fatalf("latest = %+v, want the new #2", latest)
	}
}
