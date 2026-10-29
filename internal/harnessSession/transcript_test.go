package harnessSession

import (
	"testing"

	"github.com/mattthew/optine/internal/dataTypes"
)

func TestOllamaTranscriptKeepsOnlyConversation(t *testing.T) {
	sess := &Session{
		Kind: OllamaNative,
		ollama: []*dataTypes.NativeTooltypeMessage{
			{Role: "system", Content: "system prompt"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "checking files", ToolCalls: []dataTypes.NativeTypeToolCall{{ID: "call_1"}}},
			{Role: "tool", ToolCallID: "call_1", Content: "tool result"},
			{Role: "assistant", Content: "answer"},
			{Role: "user", Content: nativeRetryPrefix + " — re-issue"},
			{Role: "user", Content: "   "},
		},
	}

	entries := sess.Transcript()

	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(entries), entries)
	}

	if entries[0].Role != TranscriptUser || entries[0].Content != "hello" {
		t.Fatalf("first entry wrong: %+v", entries[0])
	}

	if entries[1].Role != TranscriptAssistant || entries[1].Content != "answer" {
		t.Fatalf("second entry wrong: %+v", entries[1])
	}
}

func TestVLLMTranscriptKeepsOnlyConversation(t *testing.T) {
	sess := &Session{
		Kind: VLLMNative,
		vllm: []*dataTypes.VLLMTooltypeMessage{
			{Role: "system", Content: "system prompt"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "checking files", ToolCalls: []dataTypes.VLLMTypeToolCall{{ID: "call_1"}}},
			{Role: "tool", ToolCallID: "call_1", Content: "tool result"},
			{Role: "assistant", Content: "answer"},
		},
	}

	entries := sess.Transcript()

	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(entries), entries)
	}

	if entries[0].Role != TranscriptUser || entries[0].Content != "hello" {
		t.Fatalf("first entry wrong: %+v", entries[0])
	}

	if entries[1].Role != TranscriptAssistant || entries[1].Content != "answer" {
		t.Fatalf("second entry wrong: %+v", entries[1])
	}
}

func TestEnvelopeTranscriptUnwrapsFinalAnswers(t *testing.T) {
	sess := &Session{
		Kind: Envelope,
		envelope: []*dataTypes.NonNativeMessage{
			{Role: "system", Content: "system prompt"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: `{"type":"tool_call","tool":"bash","arguments":{"command":"ls"}}`},
			{Role: "user", Content: "TOOL_RESULT\ntool: bash\n\nresult:\nfile.go"},
			{Role: "assistant", Content: "```json\n{\"type\":\"final_answer\",\"content\":\"done\"}\n```"},
			{Role: "user", Content: envelopeRetryPrefix + " Reply with exactly one valid JSON object."},
		},
	}

	entries := sess.Transcript()

	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(entries), entries)
	}

	if entries[0].Role != TranscriptUser || entries[0].Content != "hello" {
		t.Fatalf("first entry wrong: %+v", entries[0])
	}

	if entries[1].Role != TranscriptAssistant || entries[1].Content != "done" {
		t.Fatalf("second entry wrong: %+v", entries[1])
	}
}

func TestTranscriptEmptyAndUnknown(t *testing.T) {
	if entries := (*Session)(nil).Transcript(); entries != nil {
		t.Fatalf("nil session returned %+v", entries)
	}

	if entries := (&Session{Kind: Kind("other")}).Transcript(); entries != nil {
		t.Fatalf("unknown kind returned %+v", entries)
	}

	if entries := (&Session{Kind: Envelope}).Transcript(); len(entries) != 0 {
		t.Fatalf("empty session returned %+v", entries)
	}
}
