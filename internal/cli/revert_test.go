package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mattthew/optine/internal/harnessSession"
	"github.com/mattthew/optine/internal/harnessSnapshotter"
)

func writeRevertTestFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRevertLatestSnapshotUsesSessionBackend(t *testing.T) {
	for _, useGit := range []bool{false, true} {
		t.Run(fmt.Sprintf("git=%t", useGit), func(t *testing.T) {
			if useGit && !harnessSnapshotter.GitAvailable() {
				t.Skip("git is not available")
			}

			work := t.TempDir()
			configDir := t.TempDir()
			sess := &harnessSession.Session{
				UID:               "session-1",
				Directory:         work,
				GitStatus:         useGit,
				TotalUserMessages: 1,
			}

			storeName := "nonGitBasedSnapshots"
			track := func(id string) error {
				return harnessSnapshotter.Track(work, filepath.Join(configDir, storeName), id)
			}
			if useGit {
				storeName = "gitBasedSnapshots"
				track = func(id string) error {
					return harnessSnapshotter.GitTrack(work, filepath.Join(configDir, storeName), id)
				}
			}

			writeRevertTestFile(t, filepath.Join(work, "keep.txt"), "original\n")
			writeRevertTestFile(t, filepath.Join(work, "gone.txt"), "delete me\n")

			if err := track(sess.UID + "-before-1"); err != nil {
				t.Fatalf("track before: %v", err)
			}

			writeRevertTestFile(t, filepath.Join(work, "keep.txt"), "changed\n")
			if err := os.Remove(filepath.Join(work, "gone.txt")); err != nil {
				t.Fatal(err)
			}
			writeRevertTestFile(t, filepath.Join(work, "created.txt"), "new\n")

			if err := track(sess.UID + "-after-1"); err != nil {
				t.Fatalf("track after: %v", err)
			}

			reverted, err := revertLatestSnapshot(sess, work, configDir)
			if err != nil {
				t.Fatalf("revertLatestSnapshot: %v", err)
			}

			want := []string{"created.txt", "gone.txt", "keep.txt"}
			if !slices.Equal(reverted, want) {
				t.Fatalf("reverted paths = %v, want %v", reverted, want)
			}

			got, err := os.ReadFile(filepath.Join(work, "keep.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "original\n" {
				t.Fatalf("keep.txt = %q, want original", got)
			}

			got, err = os.ReadFile(filepath.Join(work, "gone.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "delete me\n" {
				t.Fatalf("gone.txt = %q, want recreated content", got)
			}

			if _, err := os.Stat(filepath.Join(work, "created.txt")); !os.IsNotExist(err) {
				t.Fatalf("created.txt should be removed, err=%v", err)
			}
		})
	}
}

func TestRevertLatestSnapshotNeedsCompletedPair(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, work, configDir, uid string)
	}{
		{
			name:  "no snapshots",
			setup: func(t *testing.T, work, configDir, uid string) {},
		},
		{
			name: "only before",
			setup: func(t *testing.T, work, configDir, uid string) {
				if err := harnessSnapshotter.Track(work, filepath.Join(configDir, "nonGitBasedSnapshots"), uid+"-before-1"); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "only after",
			setup: func(t *testing.T, work, configDir, uid string) {
				if err := harnessSnapshotter.Track(work, filepath.Join(configDir, "nonGitBasedSnapshots"), uid+"-after-1"); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			work := t.TempDir()
			configDir := t.TempDir()
			sess := &harnessSession.Session{
				UID:               "session-1",
				Directory:         work,
				TotalUserMessages: 1,
			}

			tc.setup(t, work, configDir, sess.UID)

			if _, err := revertLatestSnapshot(sess, work, configDir); !errors.Is(err, errNoRevertSnapshotPair) {
				t.Fatalf("err = %v, want errNoRevertSnapshotPair", err)
			}
		})
	}
}
