package harnessSnapshotter

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// CalculateSnapshotLatestID returns the newest completed snapshot pair for one
// session: the after ID and its matching before ID. If the newest run only has
// a before snapshot, latest equals before. Missing IDs are empty strings.
func CalculateSnapshotLatestID(dir, sessUID string, gitStatus bool) (string, string) {
	snapshotsDir := filepath.Join(dir, "manifests")
	if gitStatus {
		snapshotsDir = filepath.Join(dir, "trees")
	}

	entries, err := os.ReadDir(snapshotsDir)
	if err != nil {
		return "", ""
	}

	beforePrefix := sessUID + "-before-"
	afterPrefix := sessUID + "-after-"

	maxCount := -1
	hasBefore, hasAfter := false, false // which files exist for maxCount

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		isAfter := false
		countStr, ok := strings.CutPrefix(entry.Name(), beforePrefix)
		if !ok {
			countStr, ok = strings.CutPrefix(entry.Name(), afterPrefix)
			if !ok {
				continue
			}
			isAfter = true
		}

		// non-git snapshots are stored as <id>.json, git ones have no extension
		countStr = strings.TrimSuffix(countStr, ".json")

		count, err := strconv.Atoi(countStr)
		if err != nil || count < 0 {
			continue
		}

		if count > maxCount {
			maxCount = count
			hasBefore, hasAfter = false, false
		}
		if count == maxCount {
			if isAfter {
				hasAfter = true
			} else {
				hasBefore = true
			}
		}
	}

	if maxCount < 0 {
		return "", ""
	}

	beforeLatest := ""
	if hasBefore {
		beforeLatest = fmt.Sprintf("%s-before-%d", sessUID, maxCount)
	}

	latest := beforeLatest
	if hasAfter {
		latest = fmt.Sprintf("%s-after-%d", sessUID, maxCount)
	}

	return latest, beforeLatest
}
