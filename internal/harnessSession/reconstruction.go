package harnessSession

import (
	"fmt"
)

// IsUserTurn reports whether msg is a message the user typed, as opposed to a
// tool result, retry correction or interrupt notice stored with role "user".
// Compaction counts turns with it so they match TotalUserMessages.
func IsUserTurn(msg Message) bool {
	return isUserTurn(msg)
}

func ReconstructSession(sess *Session) (*Session, error) {
	if sess == nil {
		return nil, fmt.Errorf("cannot reconstruct nil session")
	}

	// No persisted compaction checkpoint.
	if sess.Compaction.Summary == "" {
		return nil, fmt.Errorf("this session has not been compacted before")
	}

	throughTurn := sess.Compaction.ThroughTurn

	if throughTurn <= 0 {
		return nil, fmt.Errorf("invalid compaction checkpoint: throughTurn must be greater than zero")
	}

	// Find the first user message AFTER the compacted turns.
	userTurns := 0
	start := len(sess.Messages)

	for i, msg := range sess.Messages {
		if !isUserTurn(msg) {
			continue
		}

		userTurns++

		if userTurns == throughTurn+1 {
			start = i
			break
		}
	}

	if userTurns < throughTurn {
		return nil, fmt.Errorf(
			"compaction checkpoint references turn %d, but session only contains %d user turns",
			throughTurn,
			userTurns,
		)
	}

	// Create a separate session for inference.
	// The original session remains untouched.
	reconstructed := *sess

	reconstructed.Messages = make([]Message, 0, len(sess.Messages)-start+1)

	// Insert the synthetic summary.
	reconstructed.Messages = append(reconstructed.Messages, Message{
		Role:    "assistant",
		Content: sess.Compaction.Summary,
	})

	// Preserve all messages belonging to the remaining turns.
	reconstructed.Messages = append(
		reconstructed.Messages,
		sess.Messages[start:]...,
	)

	return &reconstructed, nil
}
