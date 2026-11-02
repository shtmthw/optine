package harnessCore

import (
	"encoding/json"
	"testing"

	"github.com/mattthew/optine/internal/harnessSession"
)

func TestOllamaReqBodyConvertsUnifiedHistory(t *testing.T) {
	msgs := []harnessSession.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []harnessSession.ToolCall{{ID: "call_1", Name: "bash", Arguments: map[string]any{"command": "ls"}}}},
		{Role: "tool", ToolCallID: "call_1", Content: "result"},
	}

	req := produceOllamaReqBody(msgs, "m", "sys")

	if len(req.Messages) != 4 {
		t.Fatalf("messages = %d, want 4", len(req.Messages))
	}
	if req.Messages[0].Role != "system" || req.Messages[0].Content != "sys" {
		t.Fatalf("system prompt not prepended: %+v", req.Messages[0])
	}

	call := req.Messages[2].ToolCalls[0]
	if call.Function.Name != "bash" || call.Function.Arguments["command"] != "ls" {
		t.Fatalf("tool call lost: %+v", call)
	}
	if req.Messages[3].ToolCallID != "call_1" {
		t.Fatalf("tool id lost: %+v", req.Messages[3])
	}
}

func TestOpenAIReqBodyRepairsMissingToolIDs(t *testing.T) {
	msgs := []harnessSession.Message{
		{Role: "assistant", ToolCalls: []harnessSession.ToolCall{{Name: "bash", Arguments: map[string]any{"command": "ls"}}}},
		{Role: "tool", Content: "result"},
	}

	req := produceOpenAIReqBody(msgs, "m", "sys")

	call := req.Messages[1].ToolCalls[0]
	if call.ID == "" {
		t.Fatal("missing tool call id was not synthesized")
	}
	if call.Type != "function" {
		t.Fatalf("tool call type = %q, want function", call.Type)
	}
	if req.Messages[2].ToolCallID != call.ID {
		t.Fatalf("tool reply id = %q, want %q", req.Messages[2].ToolCallID, call.ID)
	}

	var args map[string]any
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		t.Fatalf("arguments are not a JSON string: %v", err)
	}
	if args["command"] != "ls" {
		t.Fatalf("arguments lost: %+v", args)
	}
}

func TestNonNativeReqBodyMapsToolRoleToUser(t *testing.T) {
	msgs := []harnessSession.Message{
		{Role: "assistant", ToolCalls: []harnessSession.ToolCall{{ID: "call_1", Name: "bash"}}},
		{Role: "tool", ToolCallID: "call_1", Content: "result"},
	}

	req := produceNonNativeReqBody(msgs, "m", "sys")

	if len(req.Messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(req.Messages))
	}
	if req.Messages[0].Role != "system" {
		t.Fatalf("system prompt not prepended: %+v", req.Messages[0])
	}
	if req.Messages[2].Role != "user" || req.Messages[2].Content != "result" {
		t.Fatalf("tool result not mapped to user: %+v", req.Messages[2])
	}
}

func TestEncodeOpenAIArgumentsEmpty(t *testing.T) {
	if got := encodeOpenAIArguments(nil); got != "{}" {
		t.Fatalf("nil arguments = %q, want {}", got)
	}
}
