package harnessCore

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"

	"github.com/mattthew/optine/internal/dataTypes"
	"github.com/mattthew/optine/internal/harnessPermissions"
	"github.com/mattthew/optine/internal/harnessSession"
	"github.com/mattthew/optine/internal/harnessSnapshotter"
)

// AgentLoop runs one user message against the session's history.
func AgentLoop(ctx context.Context, sess *harnessSession.Session, agentConfig dataTypes.AgentConfig, userMessage string, reader *bufio.Reader) (string, error) {
	// A format mismatch would append into the wrong slot.
	if err := sess.Resumeable(harnessSession.KindFor(agentConfig.Provider, agentConfig.NativeToolCalling)); err != nil {
		return "", err
	}

	configDir := filepath.Dir(harnessPermissions.AuthPath)
	userMessageCount := sess.TotalUserMessages + 1

	// Fix the backend for this run. Resuming a session restores GitStatus from
	// disk and never re-detects git, so both captures must use the same choice.
	useGit := sess.GitStatus

	// takes a snapshot of the filesystem before running the userMessage thru the agent

	snapshotID := fmt.Sprintf("%s-before-%d", sess.UID, userMessageCount)
	switch useGit {
	case true:
		if err := harnessSnapshotter.GitTrack(sess.Directory, filepath.Join(configDir, "gitBasedSnapshots"), snapshotID); err != nil {
			log.Printf("Git tracking %s: %v", sess.UID, err)
			return "", err
		}
	case false:
		if err := harnessSnapshotter.Track(sess.Directory, filepath.Join(configDir, "nonGitBasedSnapshots"), snapshotID); err != nil {
			log.Printf("File tracking %s: %v", sess.UID, err)
			return "", err
		}
	}

	sess.BeginRun()

	// Rebuild the system message only when its inputs changed.
	if sess.SyncSystem() {
		log.Printf("session %s: system prompt changed — system prompt refreshed", sess.UID)
	}

	sess.AppendUser(userMessage)

	var (
		result string
		err    error
	)

	switch agentConfig.NativeToolCalling {
	case true:
		result, err = nativeToolAgentCall(ctx, sess, reader, agentConfig.Provider, agentConfig.Model)

	case false:
		result, err = nonNativeAgentCall(ctx, sess, reader, agentConfig.Provider, agentConfig.Model)
	}

	if commitErr := harnessSession.Commit(sess); commitErr != nil {
		log.Printf("session %s: could not save history: %v", sess.UID, commitErr)
	}

	// takes another snapshot right after the agent is finished processing users request

	snapshotIDAfter := fmt.Sprintf("%s-after-%d", sess.UID, userMessageCount)

	switch useGit {
	case true:
		if err := harnessSnapshotter.GitTrack(sess.Directory, filepath.Join(configDir, "gitBasedSnapshots"), snapshotIDAfter); err != nil {
			log.Printf("Git tracking %s: %v", sess.UID, err)
			return "", err
		}
	case false:
		if err := harnessSnapshotter.Track(sess.Directory, filepath.Join(configDir, "nonGitBasedSnapshots"), snapshotIDAfter); err != nil {
			log.Printf("File tracking %s: %v", sess.UID, err)
			return "", err
		}
	}

	if err != nil {
		if !errors.Is(err, context.Canceled) {
			return "", err
		}
	}
	return result, nil
}
