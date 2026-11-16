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

// IsCompacted reports whether the model sees the session through a summary.
func (s *Session) IsCompacted() bool {
	return s != nil && len(s.Compactions) > 0
}

// LatestCompaction is the compaction the reconstructed session is built from.
func (s *Session) LatestCompaction() (Compaction, bool) {
	if !s.IsCompacted() {
		return Compaction{}, false
	}

	return s.Compactions[len(s.Compactions)-1], true
}

// AddCompaction records summary, covering user turns 1 through throughTurn, as
// the session's next numbered compaction and returns it. throughTurn must be
// past the latest compaction's; CompactSession checks that before summarizing.
func (s *Session) AddCompaction(summary string, throughTurn int) Compaction {
	c := Compaction{Number: len(s.Compactions) + 1, Summary: summary, ThroughTurn: throughTurn}
	s.Compactions = append(s.Compactions, c)

	return c
}

// CompactionsKept is how many compactions survive a rewind to user turn
// number: the ones covering only turns before it. ThroughTurn grows with each
// compaction, so the survivors are always the oldest ones.
func (s *Session) CompactionsKept(number int) int {
	for i, c := range s.Compactions {
		if c.ThroughTurn >= number {
			return i
		}
	}

	return len(s.Compactions)
}

// ReconstructSession builds the reconstructed session (reSess) the model is
// sent for a compacted session: the latest compaction's summary followed by
// every message after the turns it covers.
func ReconstructSession(sess *Session) (*Session, error) {
	if sess == nil {
		return nil, fmt.Errorf("cannot reconstruct nil session")
	}

	latest, ok := sess.LatestCompaction()
	if !ok {
		return nil, fmt.Errorf("this session has not been compacted before")
	}

	if latest.Summary == "" {
		return nil, fmt.Errorf("invalid compaction #%d: it has no summary", latest.Number)
	}

	throughTurn := latest.ThroughTurn

	if throughTurn <= 0 {
		return nil, fmt.Errorf("invalid compaction #%d: throughTurn must be greater than zero", latest.Number)
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
			"compaction #%d covers turn %d, but session only contains %d user turns",
			latest.Number,
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
		Content: latest.Summary,
	})

	// Preserve all messages belonging to the remaining turns.
	reconstructed.Messages = append(
		reconstructed.Messages,
		sess.Messages[start:]...,
	)

	return &reconstructed, nil
}
