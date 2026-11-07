package tui

import (
	"github.com/mattthew/optine/internal/harnessSession"
)

// Resume display caps: the persisted history is never trimmed, only what is
// redrawn when a session is loaded or swapped.
const (
	maxResumeMessages = 200
	maxResumeBytes    = 1 << 20
)

// TranscriptMessages converts a saved transcript into the messages shown after
// a resume, mapping roles and applying the display caps.
func TranscriptMessages(entries []harnessSession.TranscriptEntry) []Message {
	messages := make([]Message, 0, len(entries))

	for _, entry := range entries {
		var msg Message

		switch entry.Role {
		case harnessSession.TranscriptUser:
			msg = Message{
				Role:    roleUser,
				Content: entry.Content,
			}

		case harnessSession.TranscriptAssistant:
			msg = Message{
				Role:    roleAgent,
				Content: entry.Content,
			}

		case harnessSession.TranscriptToolCall:
			msg = Message{
				Role:    roleToolCall,
				Content: entry.Content,
			}

		case harnessSession.TranscriptToolResult:
			msg = Message{
				Role:    roleToolResult,
				Content: entry.Content,
			}

		case harnessSession.TranscriptEvent:
			msg = Message{
				Role:    roleEvent,
				Content: entry.Content,
			}

		default:
			continue
		}

		messages = append(messages, msg)
	}

	return tailMessages(messages)
}

// tailMessages keeps the newest entries, retaining an oversized newest message.
func tailMessages(messages []Message) []Message {
	start := len(messages)
	size := 0

	for start > 0 && len(messages)-start < maxResumeMessages {
		next := size + len(messages[start-1].Content)

		if next > maxResumeBytes && start < len(messages) {
			break
		}

		size = next
		start--
	}

	return messages[start:]
}
