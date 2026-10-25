package harnessPermissions

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------- helpers

func mustResolve(t *testing.T, dir, p string) string {
	t.Helper()
	rp, err := resolvePath(dir, p)
	if err != nil {
		t.Fatalf("resolvePath(%q, %q): %v", dir, p, err)
	}
	return rp
}

// hasPath reports whether any action touched want (absolute, resolved).
func hasPath(actions []Action, want string) bool {
	for _, a := range actions {
		for _, p := range a.Paths {
			if p.Path == want {
				return true
			}
		}
	}
	return false
}

func mustClassify(t *testing.T, cmd, dir string) []Action {
	t.Helper()
	acts, err := ClassifyCommand(cmd, dir)
	if err != nil {
		t.Fatalf("ClassifyCommand(%q): unexpected error: %v", cmd, err)
	}
	return acts
}

// ---------------------------------------------------------------- classes

func TestClassifySimpleCommandClasses(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		cmd  string
		want Class
	}{
		{"ls", Read},
		{"cat a.txt", Read},
		{"head a.txt", Read},
		{"grep pat a.txt", Read},
		{"mkdir d", Write},
		{"touch d", Write},
		{"rm a.txt", Delete},
		{"rmdir d", Delete},
		{"mv a b", Write | Delete},
		{"cp a b", Read | Write},
		{"curl https://example.invalid", Network | Exec},
		{"wget https://example.invalid", Network | Exec},
		{"unknowncmd --flag", Exec},
		{"./script.sh", Exec},
		{"/bin/rm x", Exec},
		{"echo hi", 0}, // NoPaths: nothing to touch
		{"pwd", 0},
		{"true", 0},
	}

	for _, tc := range cases {
		t.Run(tc.cmd, func(t *testing.T) {
			acts := mustClassify(t, tc.cmd, dir)
			if len(acts) != 1 {
				t.Fatalf("want 1 action, got %d", len(acts))
			}
			if acts[0].Class != tc.want {
				t.Fatalf("class = %s, want %s", ClassNames(acts[0].Class), ClassNames(tc.want))
			}
		})
	}
}

// ---------------------------------------------------------------- paths

func TestClassifyResolvesArgumentPaths(t *testing.T) {
	dir := t.TempDir()

	acts := mustClassify(t, "cat a.txt", dir)
	if want := mustResolve(t, dir, "a.txt"); !hasPath(acts, want) {
		t.Fatalf("expected path %q, got %+v", want, acts[0].Paths)
	}
}

func TestClassifyImplicitCwdForLsAndFind(t *testing.T) {
	dir := t.TempDir()
	cwd := mustResolve(t, dir, ".")

	for _, cmd := range []string{"ls", "find"} {
		acts := mustClassify(t, cmd, dir)
		if !hasPath(acts, cwd) {
			t.Fatalf("%q should act on the cwd %q, got %+v", cmd, cwd, acts[0].Paths)
		}
	}
}

func TestClassifyGrepAlwaysAddsCwd(t *testing.T) {
	dir := t.TempDir()
	acts := mustClassify(t, "grep pat a.txt", dir)
	if !hasPath(acts, mustResolve(t, dir, ".")) {
		t.Fatalf("grep should include the cwd, got %+v", acts[0].Paths)
	}
}

func TestClassifyQuotedAndHomeWords(t *testing.T) {
	dir := t.TempDir()

	acts := mustClassify(t, `cat "my file.txt"`, dir)
	if want := mustResolve(t, dir, "my file.txt"); !hasPath(acts, want) {
		t.Fatalf("double-quoted path: want %q, got %+v", want, acts[0].Paths)
	}

	acts = mustClassify(t, `cat 'a b.txt'`, dir)
	if want := mustResolve(t, dir, "a b.txt"); !hasPath(acts, want) {
		t.Fatalf("single-quoted path: want %q, got %+v", want, acts[0].Paths)
	}

	// ~ expands to the real home dir; the word must resolve without error.
	acts = mustClassify(t, "cat ~/optine-probe.txt", dir)
	if len(acts) != 1 || len(acts[0].Paths) == 0 {
		t.Fatalf("~ should resolve to a path, got %+v", acts)
	}
}

// ---------------------------------------------------------------- redirects

func TestClassifyRedirects(t *testing.T) {
	dir := t.TempDir()

	acts := mustClassify(t, "echo hi > out.txt", dir)
	if acts[0].Class != Write {
		t.Fatalf("redirect out should be Write, got %s", ClassNames(acts[0].Class))
	}
	if want := mustResolve(t, dir, "out.txt"); !hasPath(acts, want) {
		t.Fatalf("want redirect target %q, got %+v", want, acts[0].Paths)
	}

	acts = mustClassify(t, "cat a.txt < in.txt", dir)
	if !hasPath(acts, mustResolve(t, dir, "in.txt")) {
		t.Fatalf("want redirect source in.txt, got %+v", acts[0].Paths)
	}

	// /dev/null and fd duplication are not worth a prompt, so they add no path.
	acts = mustClassify(t, "cat a.txt 2>/dev/null", dir)
	if hasPath(acts, "/dev/null") {
		t.Fatalf("/dev/null should be dropped, got %+v", acts[0].Paths)
	}

	acts = mustClassify(t, "cat a.txt 2>&1", dir)
	for _, p := range acts[0].Paths {
		if p.Path == "1" {
			t.Fatalf("fd duplication should be dropped, got %+v", acts[0].Paths)
		}
	}

	// A statement that is only a redirect has no argv.
	acts = mustClassify(t, "> only.txt", dir)
	if len(acts) != 1 || acts[0].Class != Write {
		t.Fatalf("redirect-only statement: got %+v", acts)
	}
	if want := mustResolve(t, dir, "only.txt"); !hasPath(acts, want) {
		t.Fatalf("want %q, got %+v", want, acts[0].Paths)
	}
}

// ---------------------------------------------------------------- chaining

func TestClassifyCdTracksDir(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	acts := mustClassify(t, "cd sub && cat f.txt", dir)
	if len(acts) != 2 {
		t.Fatalf("want 2 actions, got %d", len(acts))
	}
	wantDir := mustResolve(t, dir, "sub")
	if acts[1].Dir != wantDir {
		t.Fatalf("second action dir = %q, want %q", acts[1].Dir, wantDir)
	}
	if !hasPath(acts[1:], filepath.Join(wantDir, "f.txt")) {
		t.Fatalf("cat should resolve under %q, got %+v", wantDir, acts[1].Paths)
	}
}

func TestClassifyPipelineIsTwoActions(t *testing.T) {
	dir := t.TempDir()
	acts := mustClassify(t, "cat a.txt | grep pat", dir)
	if len(acts) != 2 {
		t.Fatalf("want 2 pipeline stages, got %d", len(acts))
	}
}

func TestClassifyFindFlagsBumpClass(t *testing.T) {
	dir := t.TempDir()

	acts := mustClassify(t, "find . -name x -delete", dir)
	if acts[0].Class&Delete == 0 {
		t.Fatalf("find -delete should be Delete, got %s", ClassNames(acts[0].Class))
	}

	acts = mustClassify(t, "find . -exec echo {} ';'", dir)
	if acts[0].Class&Exec == 0 {
		t.Fatalf("find -exec should be Exec, got %s", ClassNames(acts[0].Class))
	}
}

// ---------------------------------------------------------------- globs

func TestClassifyGlobResolvesToParentDir(t *testing.T) {
	dir := t.TempDir()
	build := filepath.Join(dir, "build")
	if err := os.Mkdir(build, 0o755); err != nil {
		t.Fatal(err)
	}

	acts := mustClassify(t, "rm build/*.o", dir)
	if want := mustResolve(t, dir, "build"); !hasPath(acts, want) {
		t.Fatalf("glob should resolve to parent %q, got %+v", want, acts[0].Paths)
	}

	// A bare glob under the cwd resolves to the cwd itself.
	acts = mustClassify(t, "rm *.o", dir)
	if !hasPath(acts, mustResolve(t, dir, ".")) {
		t.Fatalf("bare glob should resolve to the cwd, got %+v", acts[0].Paths)
	}
}

// ---------------------------------------------------------------- rejections

func TestClassifyRejectsUnanalyzableCommands(t *testing.T) {
	dir := t.TempDir()
	rejected := []string{
		"",                        // empty
		"echo $(whoami)",          // command substitution
		"echo `id`",               // backticks
		"echo $HOME",              // variable expansion
		"FOO=bar ls",              // assignment prefix
		"ls &",                    // background
		"(ls)",                    // subshell
		"for i in a; do ls; done", // loop
		"cat <<EOF\nx\nEOF",       // heredoc
		"echo {a,b}",              // brace expansion
		"ls -n/path",              // flag smuggling a path
	}
	for _, cmd := range rejected {
		if _, err := ClassifyCommand(cmd, dir); err == nil {
			t.Fatalf("ClassifyCommand(%q) should have been rejected", cmd)
		}
	}
}

func TestClassifyCdRejections(t *testing.T) {
	dir := t.TempDir()
	for _, cmd := range []string{"cd", "cd -", "cd a b", "cd ~user"} {
		if _, err := ClassifyCommand(cmd, dir); err == nil {
			t.Fatalf("ClassifyCommand(%q) should have been rejected", cmd)
		}
	}
}

// ---------------------------------------------------------------- path helpers

func TestResolvePathFollowsSymlinkAndRejectsDangling(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got, err := resolvePath(dir, "link.txt")
	if err != nil {
		t.Fatalf("resolving a valid symlink: %v", err)
	}
	if got != mustResolve(t, dir, "target.txt") {
		t.Fatalf("symlink should resolve to target, got %q", got)
	}

	dangling := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "nope"), dangling); err != nil {
		t.Fatal(err)
	}
	if _, err := resolvePath(dir, "dangling"); err == nil {
		t.Fatalf("a dangling symlink must not resolve")
	}
}

func TestResolvePathMissingTailIsLexical(t *testing.T) {
	dir := t.TempDir()
	got, err := resolvePath(dir, "a/b/c.txt")
	if err != nil {
		t.Fatalf("missing tail should resolve lexically: %v", err)
	}
	if want := filepath.Join(mustResolve(t, dir, "."), "a", "b", "c.txt"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestInsideRoot(t *testing.T) {
	root := "/a/b"
	cases := []struct {
		p    string
		want bool
	}{
		{"/a/b", true},
		{"/a/b/c", true},
		{"/a/bc", false},
		{"/a", false},
		{"/x", false},
	}
	for _, tc := range cases {
		if got := InsideRoot(root, tc.p); got != tc.want {
			t.Fatalf("InsideRoot(%q, %q) = %v, want %v", root, tc.p, got, tc.want)
		}
	}
}

func TestIsSensitive(t *testing.T) {
	cases := []struct {
		p    string
		want bool
	}{
		{"/home/u/.ssh/id_rsa", true},
		{"/home/u/.gnupg/secring", true},
		{"/home/u/.password-store/x.gpg", true},
		{"/w/.env", true},
		{"/w/.env.local", true},
		{"/w/main.go", false},
		{"/w/myenv", false},
	}
	for _, tc := range cases {
		if got := IsSensitive(tc.p); got != tc.want {
			t.Fatalf("IsSensitive(%q) = %v, want %v", tc.p, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------- policy

func TestPolicyForAction(t *testing.T) {
	root := "/root/ws"

	cases := []struct {
		name string
		a    Action
		want Decision
	}{
		{
			"read inside the workspace is allowed",
			Action{Dir: root, Class: Read, Paths: []Access{{Path: root + "/f", Mode: Read}}},
			Allow,
		},
		{
			"a sensitive path is denied outright",
			Action{Dir: root, Class: Read, Paths: []Access{{Path: root + "/.ssh/id_rsa", Mode: Read}}},
			Deny,
		},
		{
			"reading outside the workspace asks",
			Action{Dir: root, Class: Read, Paths: []Access{{Path: "/etc/passwd", Mode: Read}}},
			Ask,
		},
		{
			"running outside the workspace asks",
			Action{Dir: "/tmp", Class: Read, Paths: []Access{{Path: "/tmp/f", Mode: Read}}},
			Ask,
		},
		{
			"delete asks",
			Action{Dir: root, Class: Delete, Paths: []Access{{Path: root + "/f", Mode: Delete}}},
			Ask,
		},
		{
			"network asks",
			Action{Dir: root, Class: Network | Exec},
			Ask,
		},
		{
			"exec asks",
			Action{Dir: root, Class: Exec},
			Ask,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := PolicyForAction(tc.a, root); got != tc.want {
				t.Fatalf("PolicyForAction = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHasGrantIsExactArgvAndDir(t *testing.T) {
	a := Action{Argv: []string{"curl", "x"}, Dir: "/d", Class: Network | Exec}
	g := Grant{Argv: "curl\x00x", Dir: "/d"}

	if !HasGrant([]Grant{g}, a) {
		t.Fatalf("exact argv+dir grant should match")
	}
	if HasGrant([]Grant{{Argv: "curl\x00x", Dir: "/other"}}, a) {
		t.Fatalf("grant in a different dir must not match")
	}
	if HasGrant([]Grant{{Argv: "curl\x00y", Dir: "/d"}}, a) {
		t.Fatalf("grant with different argv must not match")
	}
}

func TestBashPolicyGrantOnlyUpgradesAsk(t *testing.T) {
	root := "/root/ws"

	ask := Action{Argv: []string{"curl", "x"}, Dir: root, Class: Network | Exec}
	grant := Grant{Argv: "curl\x00x", Dir: root}
	if d, _ := BashPolicy([]Action{ask}, root, []Grant{grant}); d != Allow {
		t.Fatalf("a granted ask should become Allow, got %v", d)
	}

	// A denied action (sensitive path) is never grantable.
	denied := Action{Argv: []string{"cat", "k"}, Dir: root, Class: Read,
		Paths: []Access{{Path: root + "/.ssh/k", Mode: Read}}}
	deniedGrant := Grant{Argv: "cat\x00k", Dir: root}
	if d, _ := BashPolicy([]Action{denied}, root, []Grant{deniedGrant}); d != Deny {
		t.Fatalf("a denied action must stay Deny even with a grant, got %v", d)
	}
}

func TestBashPolicyStrictestActionWins(t *testing.T) {
	root := "/root/ws"
	actions := []Action{
		{Dir: root, Class: Read, Paths: []Access{{Path: root + "/f", Mode: Read}}},  // Allow
		{Dir: root, Class: Read, Paths: []Access{{Path: "/etc/hosts", Mode: Read}}}, // Ask
	}
	if d, _ := BashPolicy(actions, root, nil); d != Ask {
		t.Fatalf("the strictest action should win, got %v", d)
	}
}
