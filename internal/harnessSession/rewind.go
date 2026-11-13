package harnessSession

import "strings"

// UserTurn is one message the user sent. Number counts the user's messages
// from 1, the same count AppendUser keeps in TotalUserMessages, so it is also
// the number in that run's snapshot IDs.
type UserTurn struct {
	Number  int
	Index   int // position of the message in Session.Messages
	Content string
}

// UserTurns lists the messages the user sent, oldest first. Interrupt
// markers, retry corrections and envelope tool results share the user role
// but are loop plumbing, so they are skipped.
func (s *Session) UserTurns() []UserTurn {
	if s == nil {
		return nil
	}

	var turns []UserTurn
	for i, msg := range s.Messages {
		if !isUserTurn(msg) {
			continue
		}

		turns = append(turns, UserTurn{Number: len(turns) + 1, Index: i, Content: msg.Content})
	}

	return turns
}

func isUserTurn(msg Message) bool {
	if msg.Role != "user" {
		return false
	}
	if strings.TrimSpace(msg.Content) == InterruptedNotice || isRetryNotice(msg.Content) {
		return false
	}

	_, _, toolResult := envelopeToolResult(msg.Content)

	return !toolResult
}

// RewindTo drops turn and everything after it from the history, so the next
// message continues from just before turn was sent.
func (s *Session) RewindTo(turn UserTurn) {
	if turn.Index < 0 || turn.Index > len(s.Messages) {
		return
	}

	clear(s.Messages[turn.Index:]) // so the backing array doesn't pin discarded data
	s.Messages = s.Messages[:turn.Index]
	s.TotalUserMessages = turn.Number - 1
}

// TranscriptBefore projects the history before index, which is what the
// conversation shows once it is rewound there.
func (s *Session) TranscriptBefore(index int) []TranscriptEntry {
	if s == nil {
		return nil
	}

	return transcriptOf(s.Messages[:max(0, min(index, len(s.Messages)))])
}
