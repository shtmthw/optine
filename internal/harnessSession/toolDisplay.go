package harnessSession

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/mattthew/optine/internal/harnessPermissions"
)

// DisplayEvent is one assistant or tool message emitted during a live run.
type DisplayEvent struct {
	Role    TranscriptRole
	Content string
}

var (
	displayMu       sync.RWMutex
	displayListener func(DisplayEvent)
)

// SetDisplayEventListener registers the live TUI bridge; nil clears it.
func SetDisplayEventListener(listener func(DisplayEvent)) {
	displayMu.Lock()
	displayListener = listener
	displayMu.Unlock()
}

func emitDisplay(role TranscriptRole, content string) {
	displayMu.RLock()
	listener := displayListener
	displayMu.RUnlock()

	if listener != nil {
		listener(DisplayEvent{Role: role, Content: content})
	}
}

// NotifyAssistant emits assistant text that accompanies native tool calls.
func NotifyAssistant(content string) {
	if strings.TrimSpace(content) == "" {
		return
	}
	emitDisplay(TranscriptAssistant, content)
}

// NotifyToolCall emits the canonical tool-call line for a live run.
func NotifyToolCall(name string, arguments map[string]any) {
	emitDisplay(TranscriptToolCall, formatToolCall(name, arguments))
}

// NotifyToolResult emits the canonical tool result for a live run.
func NotifyToolResult(name, result string) {
	emitDisplay(TranscriptToolResult, formatToolResult(name, result))
}

func formatToolCall(name string, arguments map[string]any) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "unknown"
	}
	if len(arguments) == 0 {
		return "tool call: " + name
	}

	encoded, err := json.Marshal(arguments)
	if err != nil {
		return fmt.Sprintf("tool call: %s %v", name, arguments)
	}

	return fmt.Sprintf("tool call: %s %s", name, encoded)
}

func formatToolResult(name, result string) string {
	result = strings.TrimRight(result, "\r\n")
	if strings.TrimSpace(result) == "" {
		return "(no output)"
	}

	if redacted, ok := redactSensitiveResult(name, result); ok {
		return redacted
	}

	return result
}

func redactSensitiveResult(name, result string) (string, bool) {
	inferred, path, ok := fileResultPath(result)
	if !ok || (name != "" && name != inferred) || !harnessPermissions.IsSensitive(path) {
		return "", false
	}
	if name == "" {
		name = inferred
	}

	return fmt.Sprintf("%s %s: applied (content hidden: sensitive file)", name, path), true
}

func fileResultPath(result string) (name, path string, ok bool) {
	line := result
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}

	var rest string
	switch {
	case strings.HasPrefix(line, "EDIT_OK path="):
		name = "edit_file"
		rest = strings.TrimPrefix(line, "EDIT_OK path=")
	case strings.HasPrefix(line, "WRITE_OK path="):
		name = "write_file"
		rest = strings.TrimPrefix(line, "WRITE_OK path=")
	default:
		return "", "", false
	}

	idx := strings.Index(rest, " dir=")
	if idx < 0 {
		return "", "", false
	}

	path = rest[:idx]
	if path == "" {
		return "", "", false
	}

	return name, path, true
}

func envelopeToolCall(raw string) (string, map[string]any, bool) {
	var envelope struct {
		Type      string          `json:"type"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}

	if err := json.Unmarshal([]byte(stripEnvelopeFences(raw)), &envelope); err != nil {
		return "", nil, false
	}
	if envelope.Type != "tool_call" || strings.TrimSpace(envelope.Tool) == "" {
		return "", nil, false
	}

	arguments, err := decodeEnvelopeArguments(envelope.Arguments)
	if err != nil {
		return envelope.Tool, nil, true
	}

	return envelope.Tool, arguments, true
}

func decodeEnvelopeArguments(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var arguments map[string]any
	if err := json.Unmarshal(raw, &arguments); err == nil {
		return arguments, nil
	}

	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	if strings.TrimSpace(encoded) == "" {
		return nil, nil
	}

	if err := json.Unmarshal([]byte(encoded), &arguments); err != nil {
		return nil, err
	}

	return arguments, nil
}

func envelopeToolResult(raw string) (string, string, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "TOOL_RESULT") {
		return "", "", false
	}

	rest := strings.TrimLeft(strings.TrimPrefix(raw, "TOOL_RESULT"), "\r\n")
	if !strings.HasPrefix(rest, "tool: ") {
		return "", "", false
	}

	rest = strings.TrimPrefix(rest, "tool: ")
	lineEnd := strings.IndexByte(rest, '\n')
	if lineEnd < 0 {
		return "", "", false
	}

	tool := strings.TrimSpace(rest[:lineEnd])
	rest = strings.TrimLeft(rest[lineEnd+1:], "\r\n")

	if rest == "result:" {
		return tool, "", true
	}

	const resultPrefix = "result:\n"
	if !strings.HasPrefix(rest, resultPrefix) {
		return "", "", false
	}

	result := strings.TrimPrefix(rest, resultPrefix)
	const footer = "\n\nUse this result to continue solving the user's request."
	if idx := strings.LastIndex(result, footer); idx >= 0 {
		result = result[:idx]
	}

	return tool, result, true
}
