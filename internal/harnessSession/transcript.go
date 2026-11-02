package harnessSession

import (
	"encoding/json"
	"strings"
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

// the next fix happens HERE
// Transcript projects the stored history into user and assistant turns.
// System prompts, tool traffic, tool-call turns and retry notices are omitted.
func (s *Session) Transcript() []TranscriptEntry {
	if s == nil {
		return nil
	}

	entries := make([]TranscriptEntry, 0, len(s.Messages))

	for _, msg := range s.Messages {
		if len(msg.ToolCalls) > 0 || msg.Role == "tool" || isRetryNotice(msg.Content) {
			continue
		}

		// The envelope protocol stores tool output as a user turn.
		if msg.Role == "user" && isToolResult(msg.Content) {
			continue
		}

		if msg.Role == "assistant" {
			switch envelopeType(msg.Content) {
			case "final_answer":
				if content, ok := envelopeAnswer(msg.Content); ok {
					entries = append(entries, TranscriptEntry{Role: TranscriptAssistant, Content: content})
				}
				continue
			case "tool_call":
				continue
			}
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

func isToolResult(content string) bool {
	return strings.HasPrefix(strings.TrimSpace(content), "TOOL_RESULT")
}

func stripEnvelopeFences(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")

	return strings.TrimSpace(raw)
}

func envelopeType(raw string) string {
	var envelope struct {
		Type string `json:"type"`
	}

	if err := json.Unmarshal([]byte(stripEnvelopeFences(raw)), &envelope); err != nil {
		return ""
	}

	return envelope.Type
}

// envelopeAnswer returns the content of a final_answer envelope.
func envelopeAnswer(raw string) (string, bool) {
	var envelope struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	}

	if err := json.Unmarshal([]byte(stripEnvelopeFences(raw)), &envelope); err != nil {
		return "", false
	}

	if envelope.Type != "final_answer" || strings.TrimSpace(envelope.Content) == "" {
		return "", false
	}

	return envelope.Content, true
}
