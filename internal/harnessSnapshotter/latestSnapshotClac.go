package harnessSnapshotter

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// SnapshotPair holds the snapshot IDs of one run. A missing ID is "".
type SnapshotPair struct {
	Count  int // the run's user message number
	Before string
	After  string
}

// idDir is where a backend keeps its snapshot IDs.
func idDir(dir string, gitStatus bool) string {
	if gitStatus {
		return filepath.Join(dir, "trees")
	}

	return filepath.Join(dir, "manifests")
}

// CalculateSnapshotPairsFrom returns the snapshot pairs of every run of one
// session numbered from or later, newest first. It selects by number rather
// than taking the newest N runs because numbers can have gaps: a user message
// that started no run has no snapshots. A run whose capture failed half way
// has one ID missing.
func CalculateSnapshotPairsFrom(dir, sessUID string, gitStatus bool, from int) []SnapshotPair {
	entries, err := os.ReadDir(idDir(dir, gitStatus))
	if err != nil {
		return nil
	}

	runs := make(map[int]SnapshotPair)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		count, isAfter, ok := parseSnapshotName(entry.Name(), sessUID)
		if !ok || count < from {
			continue
		}

		run := runs[count]
		run.Count = count
		if isAfter {
			run.After = fmt.Sprintf("%s-after-%d", sessUID, count)
		} else {
			run.Before = fmt.Sprintf("%s-before-%d", sessUID, count)
		}
		runs[count] = run
	}

	pairs := make([]SnapshotPair, 0, len(runs))
	for _, run := range runs {
		pairs = append(pairs, run)
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Count > pairs[j].Count })

	return pairs
}

// ForgetSnapshotsFrom deletes the snapshot IDs of every run of one session
// numbered from or later. Runs after a rewind reuse those numbers, and a
// stale ID left behind would be taken for the newest run. The stored file
// contents stay, as other snapshots may share them.
func ForgetSnapshotsFrom(dir, sessUID string, gitStatus bool, from int) error {
	ids := idDir(dir, gitStatus)

	entries, err := os.ReadDir(ids)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		count, _, ok := parseSnapshotName(entry.Name(), sessUID)
		if !ok || count < from {
			continue
		}

		if err := os.Remove(filepath.Join(ids, entry.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}

	return nil
}

// parseSnapshotName extracts the run counter from a snapshot file
func parseSnapshotName(name, sessUID string) (count int, isAfter, ok bool) {
	rest, found := strings.CutPrefix(name, sessUID+"-before-")
	if !found {
		rest, found = strings.CutPrefix(name, sessUID+"-after-")
		if !found {
			return 0, false, false
		}
		isAfter = true
	}

	count, err := strconv.Atoi(strings.TrimSuffix(rest, ".json"))
	if err != nil || count < 0 {
		return 0, false, false
	}

	return count, isAfter, true
}

// CalculateSnapshotLatestID returns the newest completed snapshot pair for one session
func CalculateSnapshotLatestID(dir, sessUID string, gitStatus bool) (string, string) {
	entries, err := os.ReadDir(idDir(dir, gitStatus))
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
