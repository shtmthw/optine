package harnessSession

import "testing"

func TestTranscriptKeepsToolTraffic(t *testing.T) {
	sess := &Session{
		Kind: OllamaNative,
		Messages: []Message{
			{Role: "system", Content: "system prompt"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "checking files", ToolCalls: []ToolCall{{ID: "call_1", Name: "bash"}}},
			{Role: "tool", ToolCallID: "call_1", Content: "tool result"},
			{Role: "assistant", Content: "answer"},
			{Role: "user", Content: nativeRetryPrefix + " — re-issue"},
			{Role: "user", Content: "   "},
		},
	}

	entries := sess.Transcript()

	if len(entries) != 5 {
		t.Fatalf("got %d entries, want 5: %+v", len(entries), entries)
	}

	want := []TranscriptEntry{
		{Role: TranscriptUser, Content: "hello"},
		{Role: TranscriptAssistant, Content: "checking files"},
		{Role: TranscriptToolCall, Content: "tool call: bash"},
		{Role: TranscriptToolResult, Content: "tool result"},
		{Role: TranscriptAssistant, Content: "answer"},
	}

	for i := range want {
		if entries[i] != want[i] {
			t.Fatalf("entry %d = %+v, want %+v", i, entries[i], want[i])
		}
	}
}

func TestTranscriptInterleavesMultipleToolResults(t *testing.T) {
	sess := &Session{
		Kind: OllamaNative,
		Messages: []Message{
			{Role: "user", Content: "run both"},
			{
				Role:    "assistant",
				Content: "running",
				ToolCalls: []ToolCall{
					{ID: "a", Name: "bash", Arguments: map[string]any{"command": "ls"}},
					{ID: "b", Name: "read_file", Arguments: map[string]any{"path": "x"}},
				},
			},
			{Role: "tool", ToolCallID: "a", Content: "one"},
			{Role: "tool", ToolCallID: "b", Content: "two"},
		},
	}

	entries := sess.Transcript()

	want := []TranscriptEntry{
		{Role: TranscriptUser, Content: "run both"},
		{Role: TranscriptAssistant, Content: "running"},
		{Role: TranscriptToolCall, Content: `tool call: bash {"command":"ls"}`},
		{Role: TranscriptToolResult, Content: "one"},
		{Role: TranscriptToolCall, Content: `tool call: read_file {"path":"x"}`},
		{Role: TranscriptToolResult, Content: "two"},
	}

	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(entries), len(want), entries)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Fatalf("entry %d = %+v, want %+v", i, entries[i], want[i])
		}
	}
}

func TestEnvelopeTranscriptUnwrapsToolTraffic(t *testing.T) {
	sess := &Session{
		Kind: Envelope,
		Messages: []Message{
			{Role: "system", Content: "system prompt"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: `{"type":"tool_call","tool":"bash","arguments":{"command":"ls"}}`},
			{Role: "user", Content: "TOOL_RESULT\ntool: bash\n\nresult:\nfile.go"},
			{Role: "assistant", Content: "```json\n{\"type\":\"final_answer\",\"content\":\"done\"}\n```"},
			{Role: "user", Content: envelopeRetryPrefix + " Reply with exactly one valid JSON object."},
			{Role: "user", Content: nativeRetryPrefix + " — re-issue your previous turn, exactly as before."},
		},
	}

	entries := sess.Transcript()

	want := []TranscriptEntry{
		{Role: TranscriptUser, Content: "hello"},
		{Role: TranscriptToolCall, Content: `tool call: bash {"command":"ls"}`},
		{Role: TranscriptToolResult, Content: "file.go"},
		{Role: TranscriptAssistant, Content: "done"},
	}

	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(entries), len(want), entries)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Fatalf("entry %d = %+v, want %+v", i, entries[i], want[i])
		}
	}
}

func TestEnvelopeTranscriptStripsToolResultFooter(t *testing.T) {
	sess := &Session{
		Messages: []Message{
			{Role: "assistant", Content: `{"type":"tool_call","tool":"bash","arguments":{"command":"ls"}}`},
			{Role: "user", Content: "TOOL_RESULT\ntool: bash\n\nresult:\nline one\nline two\n\nUse this result to continue solving the user's request. More instructions."},
		},
	}

	entries := sess.Transcript()

	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(entries), entries)
	}
	if entries[1].Content != "line one\nline two" {
		t.Fatalf("result = %q", entries[1].Content)
	}
}

func TestTranscriptMatchesResultRedactionToTool(t *testing.T) {
	const fileResult = "EDIT_OK path=/home/me/.ssh/id_rsa dir=/home/me/.ssh file=id_rsa lines=1-1"

	for _, tc := range []struct {
		tool string
		want string
	}{
		{"edit_file", "edit_file /home/me/.ssh/id_rsa: applied (content hidden: sensitive file)"},
		{"read_file", fileResult},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			sess := &Session{
				Messages: []Message{
					{Role: "assistant", ToolCalls: []ToolCall{
						{ID: "a", Name: tc.tool, Arguments: map[string]any{"path": "secret"}},
					}},
					{Role: "tool", ToolCallID: "a", Content: fileResult},
				},
			}

			entries := sess.Transcript()

			if len(entries) != 2 {
				t.Fatalf("unexpected transcript: %+v", entries)
			}
			if entries[1].Content != tc.want {
				t.Fatalf("result = %q, want %q", entries[1].Content, tc.want)
			}
		})
	}
}

func TestEnvelopeTranscriptEmptyToolResult(t *testing.T) {
	sess := &Session{
		Messages: []Message{
			{Role: "assistant", Content: `{"type":"tool_call","tool":"bash"}`},
			{Role: "user", Content: "TOOL_RESULT\ntool: bash\n\nresult:"},
		},
	}

	entries := sess.Transcript()

	if len(entries) != 2 || entries[1].Content != "(no output)" {
		t.Fatalf("unexpected transcript: %+v", entries)
	}
}

func TestTranscriptEmptyAndUnknown(t *testing.T) {
	if entries := (*Session)(nil).Transcript(); entries != nil {
		t.Fatalf("nil session returned %+v", entries)
	}

	if entries := (&Session{Kind: Kind("other")}).Transcript(); len(entries) != 0 {
		t.Fatalf("empty session returned %+v", entries)
	}

	if entries := (&Session{Kind: Envelope}).Transcript(); len(entries) != 0 {
		t.Fatalf("empty session returned %+v", entries)
	}
}
