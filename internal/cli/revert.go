package cli

import (
	"errors"
	"path/filepath"

	"github.com/mattthew/optine/internal/harnessSession"
	"github.com/mattthew/optine/internal/harnessSnapshotter"
)

// errNoRevertSnapshotPair means the session has no completed before/after
// snapshot pair for its most recent run, so there is nothing safe to restore.
var errNoRevertSnapshotPair = errors.New("no completed snapshot pair to revert")

// revertLatestSnapshot restores the files changed by the latest run using the
// same snapshot backend the session was created with.
func revertLatestSnapshot(sess *harnessSession.Session, workdir, configDir string) ([]string, error) {
	snapshotDir := filepath.Join(configDir, "nonGitBasedSnapshots")
	if sess.GitStatus {
		snapshotDir = filepath.Join(configDir, "gitBasedSnapshots")
	}

	latest, before := harnessSnapshotter.CalculateSnapshotLatestID(snapshotDir, sess.UID, sess.GitStatus)
	if latest == "" || before == "" || latest == before {
		return nil, errNoRevertSnapshotPair
	}

	if sess.GitStatus {
		return harnessSnapshotter.GitRevert(workdir, snapshotDir, before, latest)
	}

	return harnessSnapshotter.Revert(workdir, snapshotDir, before, latest)
}
