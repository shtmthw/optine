package harnessSnapshotter

import (
	"fmt"
	"os"
	"slices"
	"testing"
)

// Pairs are chosen by run number, not by taking the newest N, so a number
// with no run (a user message that started none) is simply absent, and a
// run whose capture failed half way keeps its one ID.
func TestCalculateSnapshotPairsFrom(t *testing.T) {
	const uid = "sess-1"

	for _, gitStatus := range []bool{false, true} {
		t.Run(fmt.Sprintf("git=%t", gitStatus), func(t *testing.T) {
			dir := t.TempDir()
			for _, id := range []string{
				uid + "-before-1", uid + "-after-1",
				uid + "-before-3", uid + "-after-3",
				uid + "-before-4",
				uid + "-before-10", uid + "-after-10",
				"other-before-5", "other-after-5",
			} {
				writeSnapshotID(t, dir, gitStatus, id)
			}

			got := CalculateSnapshotPairsFrom(dir, uid, gitStatus, 2)
			want := []SnapshotPair{
				{Count: 10, Before: uid + "-before-10", After: uid + "-after-10"},
				{Count: 4, Before: uid + "-before-4"},
				{Count: 3, Before: uid + "-before-3", After: uid + "-after-3"},
			}
			if !slices.Equal(got, want) {
				t.Fatalf("pairs from 2 = %+v, want %+v", got, want)
			}

			if got := CalculateSnapshotPairsFrom(dir, uid, gitStatus, 11); len(got) != 0 {
				t.Fatalf("pairs from 11 = %+v, want none", got)
			}
		})
	}

	if got := CalculateSnapshotPairsFrom(t.TempDir(), uid, false, 1); got != nil {
		t.Fatalf("a store with no snapshots gave %+v", got)
	}
}

// Forgetting removes only this session's IDs from the given run on, so the
// next run can reuse those numbers without a stale newer ID beside it.
func TestForgetSnapshotsFrom(t *testing.T) {
	const uid = "sess-1"

	for _, gitStatus := range []bool{false, true} {
		t.Run(fmt.Sprintf("git=%t", gitStatus), func(t *testing.T) {
			dir := t.TempDir()
			for _, id := range []string{
				uid + "-before-1", uid + "-after-1",
				uid + "-before-2", uid + "-after-2",
				uid + "-before-3",
				"other-before-2", "other-after-2",
			} {
				writeSnapshotID(t, dir, gitStatus, id)
			}

			if err := ForgetSnapshotsFrom(dir, uid, gitStatus, 2); err != nil {
				t.Fatalf("ForgetSnapshotsFrom: %v", err)
			}

			entries, err := os.ReadDir(idDir(dir, gitStatus))
			if err != nil {
				t.Fatal(err)
			}
			var left []string
			for _, entry := range entries {
				left = append(left, entry.Name())
			}

			ext := ".json"
			if gitStatus {
				ext = ""
			}
			want := []string{"other-after-2" + ext, "other-before-2" + ext, uid + "-after-1" + ext, uid + "-before-1" + ext}
			if !slices.Equal(left, want) {
				t.Fatalf("left %v, want %v", left, want)
			}

			latest, before := CalculateSnapshotLatestID(dir, uid, gitStatus)
			if latest != uid+"-after-1" || before != uid+"-before-1" {
				t.Fatalf("latest pair after forgetting = (%q, %q), want run 1", latest, before)
			}
		})
	}

	if err := ForgetSnapshotsFrom(t.TempDir(), uid, true, 1); err != nil {
		t.Fatalf("a store with no snapshots should be fine: %v", err)
	}
}
