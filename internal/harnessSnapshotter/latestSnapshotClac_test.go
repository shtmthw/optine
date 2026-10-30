package harnessSnapshotter

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeSnapshotID(t *testing.T, dir string, gitStatus bool, id string) {
	t.Helper()

	subdir := "manifests"
	if gitStatus {
		subdir = "trees"
	}

	path := filepath.Join(dir, subdir, id)
	if !gitStatus {
		path += ".json"
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The newest numeric snapshot count wins, and only a before/after pair for the
// same count is safe to revert.
func TestCalculateSnapshotLatestID(t *testing.T) {
	const uid = "sess-1"

	cases := []struct {
		name       string
		files      []string
		wantLatest string
		wantBefore string
	}{
		{
			name: "none",
		},
		{
			name:       "only before",
			files:      []string{uid + "-before-1"},
			wantLatest: uid + "-before-1",
			wantBefore: uid + "-before-1",
		},
		{
			name:       "only after",
			files:      []string{uid + "-after-1"},
			wantLatest: uid + "-after-1",
		},
		{
			name:       "complete pair",
			files:      []string{uid + "-before-1", uid + "-after-1"},
			wantLatest: uid + "-after-1",
			wantBefore: uid + "-before-1",
		},
		{
			name:       "newest pair wins",
			files:      []string{uid + "-before-1", uid + "-after-1", uid + "-before-2", uid + "-after-2"},
			wantLatest: uid + "-after-2",
			wantBefore: uid + "-before-2",
		},
		{
			name:       "newest run has only a before snapshot",
			files:      []string{uid + "-before-1", uid + "-after-1", uid + "-before-2"},
			wantLatest: uid + "-before-2",
			wantBefore: uid + "-before-2",
		},
		{
			name:       "other sessions are ignored",
			files:      []string{uid + "-before-1", uid + "-after-1", "other-before-9", "other-after-9"},
			wantLatest: uid + "-after-1",
			wantBefore: uid + "-before-1",
		},
		{
			name:       "malformed counts are ignored",
			files:      []string{uid + "-before-nope", uid + "-after-1"},
			wantLatest: uid + "-after-1",
		},
	}

	for _, gitStatus := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("%s/git=%t", tc.name, gitStatus), func(t *testing.T) {
				dir := t.TempDir()
				for _, id := range tc.files {
					writeSnapshotID(t, dir, gitStatus, id)
				}

				latest, before := CalculateSnapshotLatestID(dir, uid, gitStatus)
				if latest != tc.wantLatest || before != tc.wantBefore {
					t.Fatalf("CalculateSnapshotLatestID = (%q, %q), want (%q, %q)",
						latest, before, tc.wantLatest, tc.wantBefore)
				}
			})
		}
	}
}
