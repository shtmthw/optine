package cli

import (
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/mattthew/optine/internal/harnessSession"
	"github.com/mattthew/optine/internal/harnessSnapshotter"
)

// errNoRevertSnapshotPair means the session has no completed before/after
// snapshot pair for its most recent run, so there is nothing safe to restore.
var errNoRevertSnapshotPair = errors.New("no completed snapshot pair to revert")

// snapshotStore is the directory of the snapshot backend the session was
// created with.
func snapshotStore(sess *harnessSession.Session, configDir string) string {
	if sess.GitStatus {
		return filepath.Join(configDir, "gitBasedSnapshots")
	}

	return filepath.Join(configDir, "nonGitBasedSnapshots")
}

// restoreSnapshot puts the files that differ between snapshots from and to
// back as they were in from, and removes the files created after it.
func restoreSnapshot(gitStatus bool, workdir, store, from, to string) ([]string, error) {
	if gitStatus {
		return harnessSnapshotter.GitRevert(workdir, store, from, to)
	}

	return harnessSnapshotter.Revert(workdir, store, from, to)
}

// revertLatestSnapshot restores the files changed by the latest run using the
// same snapshot backend the session was created with.
func revertLatestSnapshot(sess *harnessSession.Session, workdir, configDir string, isRedo bool) ([]string, error) {
	store := snapshotStore(sess, configDir)

	latest, before := harnessSnapshotter.CalculateSnapshotLatestID(store, sess.UID, sess.GitStatus)
	if latest == "" || before == "" || latest == before {
		return nil, errNoRevertSnapshotPair
	}

	if isRedo {
		return restoreSnapshot(sess.GitStatus, workdir, store, latest, before)
	}

	return restoreSnapshot(sess.GitStatus, workdir, store, before, latest)
}

// restoreLatestRun runs /revert (isRedo false: put the files back as they
// were before the latest request) and /redo (isRedo true: put the latest
// request's changes back after a /revert), reporting the result in the
// agent interface.
func restoreLatestRun(sess *harnessSession.Session, configDir string, isRedo bool) {
	verb, done := "revert", "Reverted"
	if isRedo {
		verb, done = "redo", "Redone"
	}

	if sess.TotalUserMessages == 0 {
		log.Printf("Can not %s files in a session without messages.", verb)
		return
	}

	paths, err := revertLatestSnapshot(sess, sess.Directory, configDir, isRedo)
	if err != nil {
		if errors.Is(err, errNoRevertSnapshotPair) {
			log.Printf("No completed snapshot pair is available to %s.", verb)
		} else {
			log.Printf("error occurred while trying to %s in %s, err: %v", verb, sess.Directory, err)
		}
		return
	}

	if len(paths) == 0 {
		log.Printf("No file changes were made, so there was nothing to %s.", verb)
		return
	}
	var messageString strings.Builder

	for _, path := range paths {
		message := fmt.Sprintf("%s: %s", done, path)
		messageString.WriteString(message + "\n")
		log.Println(message)
	}

	sess.AppendUser("User has:\n" + messageString.String())
}
