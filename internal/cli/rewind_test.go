package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mattthew/optine/internal/harnessSession"
	"github.com/mattthew/optine/internal/harnessSnapshotter"
	"github.com/mattthew/optine/internal/tui"
)

// rewindFixture is a session in a temp working directory whose runs are
// recorded with real snapshots, the way AgentLoop numbers them.
type rewindFixture struct {
	t         *testing.T
	work      string
	configDir string
	store     string
	sess      *harnessSession.Session
}

func newRewindFixture(t *testing.T, useGit bool) *rewindFixture {
	t.Helper()
	if useGit && !harnessSnapshotter.GitAvailable() {
		t.Skip("git is not available")
	}

	f := &rewindFixture{t: t, work: t.TempDir(), configDir: t.TempDir()}
	f.sess = &harnessSession.Session{UID: "session-1", Directory: f.work, GitStatus: useGit}
	f.store = snapshotStore(f.sess, f.configDir)

	return f
}

func (f *rewindFixture) track(id string) {
	f.t.Helper()

	var err error
	if f.sess.GitStatus {
		err = harnessSnapshotter.GitTrack(f.work, f.store, id)
	} else {
		err = harnessSnapshotter.Track(f.work, f.store, id)
	}
	if err != nil {
		f.t.Fatalf("track %s: %v", id, err)
	}
}

// run records one user message whose run makes change to the working tree.
func (f *rewindFixture) run(message string, change func()) {
	f.t.Helper()

	n := f.sess.TotalUserMessages + 1
	f.track(fmt.Sprintf("%s-before-%d", f.sess.UID, n))
	f.sess.AppendUser(message)
	change()
	f.sess.Messages = append(f.sess.Messages, harnessSession.Message{Role: "assistant", Content: "done: " + message})
	f.track(fmt.Sprintf("%s-after-%d", f.sess.UID, n))
}

func (f *rewindFixture) write(name, content string) {
	writeRevertTestFile(f.t, filepath.Join(f.work, name), content)
}

func (f *rewindFixture) read(name string) string {
	data, err := os.ReadFile(filepath.Join(f.work, name))
	if err != nil {
		return "<missing>"
	}

	return string(data)
}

func (f *rewindFixture) contents() []string {
	var out []string
	for _, turn := range f.sess.UserTurns() {
		out = append(out, turn.Content)
	}

	return out
}

// Rewinding two of three messages puts the files back as they were before
// the second one, cuts the history there, and forgets the rewound snapshots
// so /revert afterwards targets the first run.
func TestRewindMessagesRestoresFilesAndHistory(t *testing.T) {
	for _, useGit := range []bool{false, true} {
		t.Run(fmt.Sprintf("git=%t", useGit), func(t *testing.T) {
			f := newRewindFixture(t, useGit)
			f.write("a.txt", "a0\n")

			f.run("one", func() { f.write("a.txt", "a1\n") })
			f.run("two", func() { f.write("a.txt", "a2\n"); f.write("b.txt", "b2\n") })
			f.run("three", func() { f.write("a.txt", "a3\n"); _ = os.Remove(filepath.Join(f.work, "b.txt")) })

			restored, err := rewindMessages(2, f.sess, f.work, f.configDir)
			if err != nil {
				t.Fatalf("rewindMessages: %v", err)
			}

			if want := []string{"a.txt", "b.txt"}; !slices.Equal(restored, want) {
				t.Fatalf("restored = %v, want %v", restored, want)
			}
			if f.read("a.txt") != "a1\n" || f.read("b.txt") != "<missing>" {
				t.Fatalf("files after rewind: a=%q b=%q, want the state before message 2", f.read("a.txt"), f.read("b.txt"))
			}

			if got := f.contents(); !slices.Equal(got, []string{"one"}) {
				t.Fatalf("user messages after rewind = %v, want [one]", got)
			}
			if len(f.sess.Messages) != 2 || f.sess.TotalUserMessages != 1 {
				t.Fatalf("history = %d messages, count %d; want 2 and 1", len(f.sess.Messages), f.sess.TotalUserMessages)
			}

			if pairs := harnessSnapshotter.CalculateSnapshotPairsFrom(f.store, f.sess.UID, useGit, 2); len(pairs) != 0 {
				t.Fatalf("rewound snapshots were kept: %+v", pairs)
			}

			restoreLatestRun(f.sess, f.configDir, false)
			if f.read("a.txt") != "a0\n" {
				t.Fatalf("/revert after the rewind: a=%q, want run 1 undone", f.read("a.txt"))
			}
		})
	}
}

// A user message that started no run (the notice /revert adds) still takes a
// number. Rewinding past it must not reach back into the run before it.
func TestRewindMessagesSkipsNumbersWithoutRuns(t *testing.T) {
	f := newRewindFixture(t, false)
	f.write("a.txt", "a0\n")

	f.run("one", func() { f.write("a.txt", "a1\n") })
	f.sess.AppendUser("User has:\nReverted: nothing\n") // message 2: no snapshots
	f.run("three", func() { f.write("a.txt", "a3\n") })

	if _, err := rewindMessages(2, f.sess, f.work, f.configDir); err != nil {
		t.Fatalf("rewindMessages: %v", err)
	}

	if f.read("a.txt") != "a1\n" {
		t.Fatalf("a.txt = %q, want run 1's change kept", f.read("a.txt"))
	}
	if got := f.contents(); !slices.Equal(got, []string{"one"}) {
		t.Fatalf("user messages after rewind = %v, want [one]", got)
	}
}

func TestRewindMessagesRejectsBadCounts(t *testing.T) {
	f := newRewindFixture(t, false)
	f.run("one", func() {})

	for _, n := range []int{0, -1, 2} {
		if _, err := rewindMessages(n, f.sess, f.work, f.configDir); err == nil {
			t.Fatalf("rewindMessages(%d) of 1 message should fail", n)
		}
	}
	if len(f.sess.UserTurns()) != 1 {
		t.Fatal("a rejected rewind changed the history")
	}
}

// stubPickRewind answers the /rewind picker with the message numbered pick
// (0 cancels) and records the rows it was offered.
func stubPickRewind(t *testing.T, pick int) *[]int {
	t.Helper()

	offered := new([]int)
	old := pickRewind
	pickRewind = func(turns []harnessSession.UserTurn) (harnessSession.UserTurn, bool) {
		*offered = (*offered)[:0]
		for _, turn := range turns {
			*offered = append(*offered, turn.Number)
		}
		for _, turn := range turns {
			if turn.Number == pick {
				return turn, true
			}
		}
		return harnessSession.UserTurn{}, false
	}
	t.Cleanup(func() { pickRewind = old })

	return offered
}

// Picking a message redraws the conversation up to it and hands its text
// back, but leaves the session itself alone until the next message.
func TestOpenRewindCutsOnlyTheView(t *testing.T) {
	f := newRewindFixture(t, false)
	f.write("a.txt", "a0\n")
	f.run("one", func() {})
	f.run("two", func() { f.write("a.txt", "a2\n") })
	f.run("three", func() {})

	offered := stubPickRewind(t, 2)

	pending, result := openRewind(f.sess, 0)

	if pending != 2 {
		t.Fatalf("pending = %d, want 2", pending)
	}
	if !slices.Equal(*offered, []int{3, 2, 1}) {
		t.Fatalf("offered %v, want newest first", *offered)
	}
	if result.Input != "two" {
		t.Fatalf("input = %q, want the picked message", result.Input)
	}

	view := tui.TranscriptMessages(f.sess.TranscriptBefore(f.sess.UserTurns()[1].Index))
	if len(result.Messages) != len(view)+1 || !slices.Equal(result.Messages[:len(view)], view) {
		t.Fatalf("view = %+v, want the transcript before message 2 and a notice", result.Messages)
	}
	if notice := result.Messages[len(view)]; notice.Role != "command" {
		t.Fatalf("last row = %+v, want the /rewind notice box", notice)
	}

	if len(f.sess.UserTurns()) != 3 || f.read("a.txt") != "a2\n" {
		t.Fatal("openRewind changed the session or files before the next message")
	}

	// A second /rewind only offers what is still on screen.
	offered = stubPickRewind(t, 0)
	if again, result := openRewind(f.sess, pending); again != 2 || result.Messages != nil {
		t.Fatalf("cancelled /rewind = (%d, %+v), want the earlier pick kept and no redraw", again, result)
	}
	if !slices.Equal(*offered, []int{1}) {
		t.Fatalf("offered %v while message 2 is pending, want [1]", *offered)
	}
}

func TestOpenRewindWithNothingToRewind(t *testing.T) {
	f := newRewindFixture(t, false)
	offered := stubPickRewind(t, 1)

	if pending, result := openRewind(f.sess, 0); pending != 0 || result.Messages != nil {
		t.Fatalf("empty session = (%d, %+v), want nothing", pending, result)
	}

	f.run("one", func() {})
	if pending, _ := openRewind(f.sess, 1); pending != 1 {
		t.Fatalf("pending = %d, want the earlier pick kept", pending)
	}
	if len(*offered) != 0 {
		t.Fatalf("the picker opened with nothing to offer: %v", *offered)
	}
}

// Applying the pending rewind rewinds the files and saves the shortened
// history, so the session file agrees with the working tree.
func TestApplyRewindSavesShortenedHistory(t *testing.T) {
	freshAuth(t)

	f := newRewindFixture(t, false)
	f.write("a.txt", "a0\n")
	f.run("one", func() { f.write("a.txt", "a1\n") })
	f.run("two", func() { f.write("a.txt", "a2\n") })
	f.sess.RunCount = 2 // Commit skips sessions that never ran

	if err := applyRewind(f.sess, f.configDir, 2); err != nil {
		t.Fatalf("applyRewind: %v", err)
	}

	if f.read("a.txt") != "a1\n" {
		t.Fatalf("a.txt = %q, want the state before message 2", f.read("a.txt"))
	}

	saved, err := harnessSession.Load(f.sess.UID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var contents []string
	for _, turn := range saved.UserTurns() {
		contents = append(contents, turn.Content)
	}
	if !slices.Equal(contents, []string{"one"}) || saved.TotalUserMessages != 1 {
		t.Fatalf("saved messages = %v (count %d), want [one] and 1", contents, saved.TotalUserMessages)
	}
}
