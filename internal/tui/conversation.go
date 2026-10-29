package tui

import "github.com/mattthew/optine/internal/harnessSession"

// Resume display caps: the persisted history is never trimmed, only what is
// redrawn when a session is loaded or swapped.
const (
	maxResumeMessages = 200
	maxResumeBytes    = 1 << 20
)

// TranscriptMessages converts a saved transcript into the messages shown after
// a resume. harnessSession.Transcript already drops tool traffic, so this only
// maps roles and applies the display caps.
func TranscriptMessages(entries []harnessSession.TranscriptEntry) []Message {
	messages := make([]Message, 0, len(entries))

	for _, entry := range entries {
		switch entry.Role {
		case harnessSession.TranscriptUser:
			messages = append(messages, Message{Role: roleUser, Content: entry.Content})
		case harnessSession.TranscriptAssistant:
			messages = append(messages, Message{Role: roleAgent, Content: entry.Content})
		}
	}

	return tailMessages(messages)
}

// tailMessages keeps the newest entries within both resume caps.
func tailMessages(messages []Message) []Message {
	start := len(messages)
	size := 0

	for start > 0 {
		if len(messages)-start == maxResumeMessages {
			break
		}

		next := size + len(messages[start-1].Content)
		if next > maxResumeBytes && start < len(messages) {
			break
		}

		size = next
		start--
	}

	return messages[start:]
}
