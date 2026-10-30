package harnessPermissions

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectApprovedDirs(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	dirs := collectApprovedDirs(root)

	for _, want := range []string{root, filepath.Join(root, "a"), nested} {
		if !dirs[want] {
			t.Fatalf("collectApprovedDirs(%q) missing %q", root, want)
		}
	}

	if dirs[t.TempDir()] {
		t.Fatal("collectApprovedDirs must not include an unrelated sibling")
	}
}

func TestReadAlwaysApprovesDirForAllFileTools(t *testing.T) {
	dir := t.TempDir()
	realDir := mustResolve(t, dir, ".")
	delete(approvedDirs, realDir)
	t.Cleanup(func() { delete(approvedDirs, realDir) })

	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Prove the directory is not already approved before testing Remember.
	if _, err := ReadFilePolicy(map[string]any{"path": path}, bufio.NewReader(strings.NewReader(""))); err == nil {
		t.Fatal("an unapproved read should prompt and fail on an empty answer")
	}
	if _, err := ReadFilePolicy(map[string]any{"path": path}, answerReader("a")); err != nil {
		t.Fatalf("Remember on read should allow: %v", err)
	}

	// The shared map means edit_file no longer prompts for this directory.
	empty := bufio.NewReader(strings.NewReader(""))
	if _, _, _, err := EditFilePolicy(map[string]any{
		"path":       path,
		"old_string": "hello",
		"new_string": "world",
	}, empty, false); err != nil {
		t.Fatalf("edit should use the shared read approval: %v", err)
	}

	empty = bufio.NewReader(strings.NewReader(""))
	if _, _, err := WriteFilePolicy(map[string]any{
		"path":    filepath.Join(dir, "new.txt"),
		"content": "hi",
	}, empty); err != nil {
		t.Fatalf("write should use the shared read approval: %v", err)
	}
}

func TestSensitiveReadAlwaysPrompts(t *testing.T) {
	root := t.TempDir()
	sshDir := filepath.Join(root, ".ssh")
	if err := os.MkdirAll(sshDir, 0o755); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(sshDir, "id_rsa")
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	realDir := mustResolve(t, sshDir, ".")
	approvedDirs[realDir] = true
	t.Cleanup(func() { delete(approvedDirs, realDir) })

	if _, err := ReadFilePolicy(map[string]any{"path": path}, answerReader("y")); err != nil {
		t.Fatalf("Once should allow a sensitive read: %v", err)
	}

	// The directory is approved, but a sensitive read must still prompt.
	empty := bufio.NewReader(strings.NewReader(""))
	if _, err := ReadFilePolicy(map[string]any{"path": path}, empty); err == nil {
		t.Fatal("a sensitive read must not be auto-approved by the shared map")
	}

	if _, err := ReadFilePolicy(map[string]any{"path": path}, answerReader("n")); !errors.Is(err, ErrReadDenied) {
		t.Fatalf("No should deny with ErrReadDenied, got %v", err)
	}

	if _, err := ReadFilePolicy(map[string]any{"path": path}, answerReader("a")); err != nil {
		t.Fatalf("Remember should allow this sensitive read: %v", err)
	}
	empty = bufio.NewReader(strings.NewReader(""))
	if _, err := ReadFilePolicy(map[string]any{"path": path}, empty); err == nil {
		t.Fatal("Remember on a sensitive read must not auto-approve later sensitive reads")
	}
}

func TestBashApprovedDirAllowsSafeActionsButStillAsksRisky(t *testing.T) {
	dir := t.TempDir()
	realDir := mustResolve(t, dir, ".")
	approvedDirs[realDir] = true
	t.Cleanup(func() { delete(approvedDirs, realDir) })

	root := "/root/ws"
	filePath := filepath.Join(realDir, "f")
	safeRead := Action{
		Dir:   realDir,
		Class: Read,
		Paths: []Access{{Path: filePath, Mode: Read}},
	}

	if d, why := PolicyForAction(safeRead, root); d != Allow {
		t.Fatalf("safe read in an approved dir = %v (%s), want Allow", d, why)
	}

	safeWrite := safeRead
	safeWrite.Class = Write
	safeWrite.Paths = []Access{{Path: filePath, Mode: Write}}
	if d, why := PolicyForAction(safeWrite, root); d != Allow {
		t.Fatalf("safe write in an approved dir = %v (%s), want Allow", d, why)
	}

	for _, tc := range []struct {
		name string
		a    Action
	}{
		{
			name: "delete",
			a: Action{
				Dir:   realDir,
				Class: Delete,
				Paths: []Access{{Path: filePath, Mode: Delete}},
			},
		},
		{
			name: "network",
			a:    Action{Dir: realDir, Class: Network | Exec},
		},
		{
			name: "exec",
			a:    Action{Dir: realDir, Class: Exec},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if d, why := PolicyForAction(tc.a, root); d != Ask {
				t.Fatalf("%s in an approved dir = %v (%s), want Ask", tc.name, d, why)
			}
		})
	}

	sensitive := Action{
		Dir:   realDir,
		Class: Read,
		Paths: []Access{{Path: filepath.Join(realDir, ".ssh", "k"), Mode: Read}},
	}
	if d, why := PolicyForAction(sensitive, root); d != Deny {
		t.Fatalf("sensitive path = %v (%s), want Deny", d, why)
	}

	outside := t.TempDir()
	outsideReal := mustResolve(t, outside, ".")
	outsideAction := Action{
		Dir:   realDir,
		Class: Read,
		Paths: []Access{{Path: filepath.Join(outsideReal, "f"), Mode: Read}},
	}
	if d, why := PolicyForAction(outsideAction, root); d != Ask {
		t.Fatalf("unapproved outside path = %v (%s), want Ask", d, why)
	}
}
