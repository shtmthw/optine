package cli

import (
	"bufio"
	"context"
	"fmt"

	"github.com/mattthew/optine/internal/harnessCore"
	"github.com/mattthew/optine/internal/harnessSession"
)

func CompactSession(reSess, sess *harnessSession.Session, toolCalling bool, provider, model string, ctx context.Context, reader *bufio.Reader) error {
	throughTurn := sess.TotalUserMessages * 80 / 100

	// An already compacted session is summarised from its reconstructed view
	// (latest summary + newer turns) so the input still fits the context
	// window. reSess counts user turns from the latest cutoff onward.
	source, turns := sess.Messages, throughTurn
	if latest, ok := sess.LatestCompaction(); ok {
		// Each compaction must cover more than the one before it, or a rewind
		// could no longer tell which ones still match the history.
		// MessageToCompact's own error is about a session with too few
		// messages, which is not what happened here.
		if throughTurn <= latest.ThroughTurn {
			return fmt.Errorf(
				"nothing new to compact: messages 1-%d are already compacted (compaction #%d) and the next cutoff has not moved past them yet",
				latest.ThroughTurn,
				latest.Number,
			)
		}

		if reSess != nil {
			source, turns = reSess.Messages, throughTurn-latest.ThroughTurn
		}
	}

	toCompact, messageCompactionErr := MessageToCompact(source, turns)

	if messageCompactionErr != nil {
		return messageCompactionErr
	}

	dummySession := *sess
	// The dummy is a plain one-off conversation; it must never take the
	// compacted branch in the tool loops.
	dummySession.Compactions = nil
	// It starts with no measured prompt, so the over-the-limit count that
	// triggered this compaction can't be read as the summary run's own.
	dummySession.LastPromptTokens = 0

	// Replace messages with the history being compacted.
	dummySession.Messages = make([]harnessSession.Message, 0, len(toCompact)+1)
	dummySession.Messages = append(dummySession.Messages, toCompact...)

	// Ask the model to compact the supplied conversation.
	dummySession.Messages = append(dummySession.Messages, harnessSession.Message{
		Role: "user",
		Content: `Compact the conversation history above into a concise but sufficiently detailed summary.

	Preserve:
	- The user's primary objectives and requirements.
	- Important architectural decisions and reasoning.
	- Completed and unfinished work.
	- Relevant technical details, files, functions and data structures.
	- Constraints, invariants, and known issues.
	- Failed approaches and unresolved questions.
	- The immediate next steps.

	Discard irrelevant conversation, repetitive explanations, and unnecessary tool output.

	The summary must contain enough information for another instance of you to continue the work without access to the original conversation.

	Return ONLY the compacted summary text. Do not include a preamble or commentary.`,
	})

	// The envelope loop only accepts a JSON envelope, so there the summary has
	// to come back as a final_answer's content, not as bare text.
	if !toolCalling {
		prompt := &dummySession.Messages[len(dummySession.Messages)-1]
		prompt.Content += "\n\nSince you reply in the JSON envelope format, return the summary as the content of exactly one final_answer object. Do not return a tool_call."
	}

	var (
		result string
		err    error
	)

	switch toolCalling {

	case true:
		// nil callback: the summary run never compacts itself.
		result, err = harnessCore.NativeToolAgentCall(ctx, nil, &dummySession, reader, provider, model, nil)
	case false:
		result, err = harnessCore.NonNativeAgentCall(ctx, &dummySession, reader, provider, model)
	}

	if err != nil {
		return err
	}
	sess.AddCompaction(result, throughTurn)

	return nil
}

func MessageToCompact(messages []harnessSession.Message, throughTurn int) ([]harnessSession.Message, error) {
	if throughTurn <= 0 {
		return nil, fmt.Errorf("cannot compact session when the user message is either one or none")
	}

	userTurns := 0
	end := len(messages)

	for i, msg := range messages {
		if !harnessSession.IsUserTurn(msg) {
			continue
		}

		userTurns++

		// Found the first user message AFTER the requested boundary.
		if userTurns == throughTurn+1 {
			end = i
			break
		}
	}

	if userTurns < throughTurn {
		return nil, fmt.Errorf(
			"requested %d turns, but only found %d",
			throughTurn,
			userTurns,
		)
	}

	// Return a separate slice containing turns 1 through throughTurn.
	result := make([]harnessSession.Message, end)
	copy(result, messages[:end])

	return result, nil
}
