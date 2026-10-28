package harnessCore

import (
	"bufio"
	"context"
	"errors"
	"log"

	"github.com/mattthew/optine/internal/dataTypes"
	"github.com/mattthew/optine/internal/harnessSession"
)

// AgentLoop runs one user message against the session's history.
//
// The session owns the conversation: it is seeded with a system message at
// creation, gets the user message appended here, and the provider loop appends
// every assistant/tool turn into it. The session is committed to disk on every
// exit path (answer, error, abort), best-effort — a bad disk never aborts a run.
func AgentLoop(ctx context.Context, sess *harnessSession.Session, agentConfig dataTypes.AgentConfig, userMessage string, reader *bufio.Reader) (string, error) {

	// The session and the loop must agree on the message format: a mismatch
	// would append into the wrong slot and silently lose the run.
	if err := sess.Resumeable(harnessSession.KindFor(agentConfig.Provider, agentConfig.NativeToolCalling)); err != nil {
		log.Printf("session %s: %v", sess.UID, err)
		return "", err
	}

	// increments on the session run count and initiates the TUI for session tracking
	sess.BeginRun()

	// Instant memory freshness: re-read the notebook, rebuild the system
	// message only when its contents actually changed.
	if sess.SyncSystem() {
		log.Printf("session %s: system prompt changed — system prompt refreshed", sess.UID)
	}

	// adds the user message to the currently active session
	sess.AppendUser(userMessage)

	var (
		result string
		err    error
	)

	switch agentConfig.NativeToolCalling {
	case true:
		// native tool path, pass the agentConfig.Provider
		result, err = nativeToolAgentCall(ctx, sess, reader, agentConfig.Provider, agentConfig.Model)

	case false:
		// envelope path pass the agentConfig.Provider
		result, err = nonNativeAgentCall(ctx, sess, reader, agentConfig.Provider, agentConfig.Model)
	}

	if commitErr := harnessSession.Commit(sess); commitErr != nil {
		log.Printf("session %s: could not save history: %v", sess.UID, commitErr)
	}

	if err != nil {
		if !errors.Is(err, context.Canceled) {
			log.Println("error occured in agent loop, err: ", err)
		}
		return "", err
	}
	return result, nil
}
