package harnessSession

import (
	"encoding/json"
	"strings"
)

// TranscriptRole is the display role of one previous turn.
type TranscriptRole string

const (
	TranscriptUser       TranscriptRole = "user"
	TranscriptAssistant  TranscriptRole = "assistant"
	TranscriptToolResult TranscriptRole = "tool"
	TranscriptToolCall   TranscriptRole = "tool_call"
	TranscriptEvent      TranscriptRole = "event"
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

// Transcript projects stored history into user, assistant and tool messages.
func (s *Session) Transcript() []TranscriptEntry {
	if s == nil {
		return nil
	}

	return transcriptOf(s.Messages)
}

func transcriptOf(messages []Message) []TranscriptEntry {
	entries := make([]TranscriptEntry, 0, len(messages))
	pending := make([]TranscriptEntry, 0, 4)
	pendingNames := make([]string, 0, 4)

	flushCalls := func() {
		entries = append(entries, pending...)
		pending = pending[:0]
		pendingNames = pendingNames[:0]
	}

	popPending := func() (TranscriptEntry, string, bool) {
		if len(pending) == 0 {
			return TranscriptEntry{}, "", false
		}
		entry := pending[0]
		name := pendingNames[0]
		pending = pending[1:]
		pendingNames = pendingNames[1:]
		return entry, name, true
	}

	for _, msg := range messages {
		if msg.Role == "user" && isRetryNotice(msg.Content) {
			continue
		}

		if msg.Role == "user" && strings.TrimSpace(msg.Content) == InterruptedNotice {
			flushCalls()
			entries = append(entries, TranscriptEntry{Role: TranscriptEvent, Content: InterruptedNotice})
			continue
		}

		if msg.Role == "user" {
			if tool, result, ok := envelopeToolResult(msg.Content); ok {
				if entry, _, ok := popPending(); ok {
					entries = append(entries, entry)
				}
				entries = append(entries, TranscriptEntry{
					Role:    TranscriptToolResult,
					Content: formatToolResult(tool, result),
				})
				continue
			}
		}

		if msg.Role == "tool" {
			name := ""
			if entry, callName, ok := popPending(); ok {
				entries = append(entries, entry)
				name = callName
			}
			entries = append(entries, TranscriptEntry{
				Role:    TranscriptToolResult,
				Content: formatToolResult(name, msg.Content),
			})
			continue
		}

		if msg.Role == "assistant" && len(msg.ToolCalls) == 0 {
			switch envelopeType(msg.Content) {
			case "final_answer":
				flushCalls()
				content, ok := envelopeAnswer(msg.Content)
				if ok {
					entries = append(entries, TranscriptEntry{
						Role:    TranscriptAssistant,
						Content: content,
					})
				}
				continue

			case "tool_call":
				flushCalls()
				tool, arguments, ok := envelopeToolCall(msg.Content)
				if !ok {
					entries = append(entries, TranscriptEntry{
						Role:    TranscriptToolCall,
						Content: msg.Content,
					})
					continue
				}
				pending = append(pending, TranscriptEntry{
					Role:    TranscriptToolCall,
					Content: formatToolCall(tool, arguments),
				})
				pendingNames = append(pendingNames, tool)
				continue
			}
		}

		if len(msg.ToolCalls) > 0 {
			flushCalls()
			if msg.Role == "assistant" && strings.TrimSpace(msg.Content) != "" {
				entries = append(entries, TranscriptEntry{
					Role:    TranscriptAssistant,
					Content: msg.Content,
				})
			}

			for _, tool := range msg.ToolCalls {
				pending = append(pending, TranscriptEntry{
					Role:    TranscriptToolCall,
					Content: formatToolCall(tool.Name, tool.Arguments),
				})
				pendingNames = append(pendingNames, tool.Name)
			}
			continue
		}

		flushCalls()
		role, ok := transcriptRole(msg.Role, msg.Content)
		if !ok {
			continue
		}

		entries = append(entries, TranscriptEntry{
			Role:    role,
			Content: msg.Content,
		})
	}

	flushCalls()

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
