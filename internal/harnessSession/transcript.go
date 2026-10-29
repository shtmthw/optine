package harnessSession

import (
	"encoding/json"
	"strings"

	"github.com/mattthew/optine/internal/dataTypes"
)

// TranscriptRole is the display role of one previous turn.
type TranscriptRole string

const (
	TranscriptUser      TranscriptRole = "user"
	TranscriptAssistant TranscriptRole = "assistant"
)

// TranscriptEntry is one displayable message recovered from a session.
type TranscriptEntry struct {
	Role    TranscriptRole
	Content string
}

// Retry corrections are loop plumbing, not conversation; keep in sync with
// harnessCore's malformed-reply notices.
const (
	nativeRetryPrefix   = "Your last reply wasn't usable JSON"
	envelopeRetryPrefix = "Your last reply was not a valid tool-call envelope."
)

// Transcript projects the active history into user and assistant turns.
// System prompts, tool traffic, tool-call turns and retry notices are omitted.
func (s *Session) Transcript() []TranscriptEntry {
	if s == nil {
		return nil
	}

	switch s.Kind {
	case OllamaNative:
		return ollamaTranscript(s.ollama)
	case VLLMNative:
		return vllmTranscript(s.vllm)
	case Envelope:
		return envelopeTranscript(s.envelope)
	}

	return nil
}

func ollamaTranscript(messages []*dataTypes.NativeTooltypeMessage) []TranscriptEntry {
	entries := make([]TranscriptEntry, 0, len(messages))

	for _, msg := range messages {
		if msg == nil || len(msg.ToolCalls) > 0 || isRetryNotice(msg.Content) {
			continue
		}

		role, ok := transcriptRole(msg.Role, msg.Content)
		if !ok {
			continue
		}

		entries = append(entries, TranscriptEntry{Role: role, Content: msg.Content})
	}

	return entries
}

func vllmTranscript(messages []*dataTypes.VLLMTooltypeMessage) []TranscriptEntry {
	entries := make([]TranscriptEntry, 0, len(messages))

	for _, msg := range messages {
		if msg == nil || len(msg.ToolCalls) > 0 || isRetryNotice(msg.Content) {
			continue
		}

		role, ok := transcriptRole(msg.Role, msg.Content)
		if !ok {
			continue
		}

		entries = append(entries, TranscriptEntry{Role: role, Content: msg.Content})
	}

	return entries
}

func transcriptRole(role, content string) (TranscriptRole, bool) {
	if strings.TrimSpace(content) == "" {
		return "", false
	}

	switch role {
	case "user":
		return TranscriptUser, true
	case "assistant":
		return TranscriptAssistant, true
	default:
		return "", false
	}
}

func isRetryNotice(content string) bool {
	content = strings.TrimSpace(content)

	return strings.HasPrefix(content, nativeRetryPrefix) ||
		strings.HasPrefix(content, envelopeRetryPrefix)
}

func envelopeTranscript(messages []*dataTypes.NonNativeMessage) []TranscriptEntry {
	entries := make([]TranscriptEntry, 0, len(messages))

	for _, msg := range messages {
		if msg == nil || isRetryNotice(msg.Content) {
			continue
		}

		switch msg.Role {
		case "user":
			// Tool output is stored as a user turn on the envelope path.
			if isToolResult(msg.Content) {
				continue
			}

			if role, ok := transcriptRole(msg.Role, msg.Content); ok {
				entries = append(entries, TranscriptEntry{Role: role, Content: msg.Content})
			}

		case "assistant":
			content, ok := envelopeAnswer(msg.Content)
			if !ok {
				continue
			}

			entries = append(entries, TranscriptEntry{Role: TranscriptAssistant, Content: content})
		}
	}

	return entries
}

func isToolResult(content string) bool {
	return strings.HasPrefix(strings.TrimSpace(content), "TOOL_RESULT")
}

// envelopeAnswer returns the content of a final_answer envelope.
func envelopeAnswer(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var envelope struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	}

	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return "", false
	}

	if envelope.Type != "final_answer" || strings.TrimSpace(envelope.Content) == "" {
		return "", false
	}

	return envelope.Content, true
}
