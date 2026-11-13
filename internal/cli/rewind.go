package cli

import (
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/mattthew/optine/internal/harnessSession"
	"github.com/mattthew/optine/internal/harnessSnapshotter"
	"github.com/mattthew/optine/internal/tui"
)

// /rewind takes the conversation back to before one of the user's messages
// in two steps. Picking the message cuts the view at once (openRewind); the
// files and the saved history follow only when the next message is sent
// (applyRewind), so leaving before then changes nothing.

// pickRewind opens the rewind picker. It is a variable so tests can drive
// /rewind without a live TUI.
var pickRewind = tui.PickRewind

// openRewind runs /rewind. pending is the message number an earlier /rewind
// picked and has not applied yet (0 if none); only the messages before it are
// still on screen, so only those are offered. It returns the message number
// now pending and the result that redraws the TUI: the conversation up to the
// picked message, with that message's text back in the input line.
func openRewind(sess *harnessSession.Session, pending int) (int, tui.AgentResult) {
	turns := sess.UserTurns()
	if pending > 0 && pending <= len(turns) {
		turns = turns[:pending-1]
	}

	if len(turns) == 0 {
		log.Println("No messages to rewind.")
		return pending, tui.AgentResult{}
	}

	newestFirst := slices.Clone(turns)
	slices.Reverse(newestFirst)

	turn, ok := pickRewind(newestFirst)
	if !ok {
		return pending, tui.AgentResult{}
	}

	messages := tui.TranscriptMessages(sess.TranscriptBefore(turn.Index))
	messages = append(messages, tui.CommandMessage("/rewind",
		fmt.Sprintf("Rewound to before message #%d; its text is back in the prompt.", turn.Number),
		"Files and the saved session are rewound when you send your next message.",
	))

	return turn.Number, tui.AgentResult{Messages: messages, Input: turn.Content}
}

// applyRewind makes the pending rewind to message number pending real, just
// before the next message runs, and saves the shortened history so the
// session file matches the restored files even if that run fails early.
func applyRewind(sess *harnessSession.Session, configDir string, pending int) error {
	messagesToRewind := len(sess.UserTurns()) - pending + 1

	restored, err := rewindMessages(messagesToRewind, sess, sess.Directory, configDir)
	if err != nil {
		return err
	}

	if err := harnessSession.Commit(sess); err != nil {
		log.Printf("session %s: could not save the rewound history: %v", sess.UID, err)
	}

	if len(restored) > 0 {
		log.Printf("Rewound %d message(s); restored %s", messagesToRewind, strings.Join(restored, ", "))
	} else {
		log.Printf("Rewound %d message(s); no files needed restoring.", messagesToRewind)
	}

	return nil
}

// rewindMessages removes the last messagesToRewind user messages, and
// everything that came after the first of them, from the history, and puts
// back the files their runs changed. Files go first: if restoring them fails,
// the history is still whole and the rewind can be tried again.
func rewindMessages(messagesToRewind int, s *harnessSession.Session, workdir, configDir string) ([]string, error) {
	turns := s.UserTurns()
	if messagesToRewind <= 0 || messagesToRewind > len(turns) {
		return nil, fmt.Errorf("cannot rewind %d of %d messages", messagesToRewind, len(turns))
	}

	cut := turns[len(turns)-messagesToRewind]

	restored, err := rewindFiles(s, workdir, configDir, cut.Number)
	if err != nil {
		return restored, err
	}

	s.RewindTo(cut)

	return restored, nil
}

// rewindFiles undoes the runs of message number from and later, newest first,
// then forgets their snapshots so the runs that reuse those numbers start
// clean. It returns the restored paths, sorted and without repeats.
func rewindFiles(s *harnessSession.Session, workdir, configDir string, from int) ([]string, error) {
	store := snapshotStore(s, configDir)

	var restored []string
	for _, pair := range harnessSnapshotter.CalculateSnapshotPairsFrom(store, s.UID, s.GitStatus, from) {
		if pair.Before == "" || pair.After == "" {
			log.Printf("Message #%d has no completed snapshot pair; its file changes cannot be restored.", pair.Count)
			continue
		}

		paths, err := restoreSnapshot(s.GitStatus, workdir, store, pair.Before, pair.After)
		restored = append(restored, paths...)
		if err != nil {
			return restored, fmt.Errorf("restoring the files of message #%d: %w", pair.Count, err)
		}
	}

	if err := harnessSnapshotter.ForgetSnapshotsFrom(store, s.UID, s.GitStatus, from); err != nil {
		return restored, fmt.Errorf("forgetting the rewound snapshots: %w", err)
	}

	slices.Sort(restored)

	return slices.Compact(restored), nil
}
