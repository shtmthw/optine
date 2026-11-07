package cli

import (
	"errors"
	"log"
	"path/filepath"

	"github.com/mattthew/optine/internal/harnessSession"
	"github.com/mattthew/optine/internal/harnessSnapshotter"
)

// errNoRevertSnapshotPair means the session has no completed before/after
// snapshot pair for its most recent run, so there is nothing safe to restore.
var errNoRevertSnapshotPair = errors.New("no completed snapshot pair to revert")

// revertLatestSnapshot restores the files changed by the latest run using the
// same snapshot backend the session was created with.
func revertLatestSnapshot(sess *harnessSession.Session, workdir, configDir string, isRedo bool) ([]string, error) {
	snapshotDir := filepath.Join(configDir, "nonGitBasedSnapshots")
	if sess.GitStatus {
		snapshotDir = filepath.Join(configDir, "gitBasedSnapshots")
	}

	latest, before := harnessSnapshotter.CalculateSnapshotLatestID(snapshotDir, sess.UID, sess.GitStatus)
	if latest == "" || before == "" || latest == before {
		return nil, errNoRevertSnapshotPair
	}

	switch isRedo {
	case false:
		if sess.GitStatus {
			return harnessSnapshotter.GitRevert(workdir, snapshotDir, before, latest)
		}

		return harnessSnapshotter.Revert(workdir, snapshotDir, before, latest)
	default:
		if sess.GitStatus {
			return harnessSnapshotter.GitRevert(workdir, snapshotDir, latest, before)
		}

		return harnessSnapshotter.Revert(workdir, snapshotDir, latest, before)
	}
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

	for _, path := range paths {
		log.Printf("%s: %s", done, path)
	}
}
