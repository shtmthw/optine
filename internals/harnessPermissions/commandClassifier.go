// First draft of the Casual Mode command classifier.
//
//	shell string → classifyCommand → []Action → policyHandler (not written here)
//
// Rules of this draft:
//   - Only a small whitelist of shell syntax is accepted (see classifyStmt).
//     Anything else returns an error and the caller denies without asking.
//   - Commands that parse fine but that we know nothing about get Class Exec
//     (the caller should ask).
//   - No methods, plain functions and structs.
package harnessPermissions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Class is a set of flags, OR them together.
type Class uint8

const (
	Read    Class = 1 << iota // reads files
	Write                     // creates or modifies files
	Delete                    // irreversible removal (or overwrite by mv)
	Network                   // outbound traffic
	Exec                      // runs code whose effects can't be known statically
)

// Access is one path a command touches, already resolved.
type Access struct {
	Path string // absolute, symlinks resolved
	Mode Class  // Read, Write and/or Delete
}

// Action is one simple command.
type Action struct {
	Argv  []string
	Dir   string // cwd this command runs in, after tracking cd
	Class Class
	Paths []Access
}

var homeDir, _ = os.UserHomeDir()
var cwd, _ = os.Getwd()

// ---------------------------------------------------------------- spec table

type spec struct {
	Class    Class
	NoPaths  bool // args are not paths (echo, pwd)
	Implicit bool // with no path args the command acts on the cwd (ls, find)
	CwdToo   bool // always add the cwd (grep: can't tell the pattern from a path)
}

// Anything not in this table is Exec. Grow it as real runs need more.
// Left out on purpose for now: git, sed, awk, sort, tar, python, make...
var specs = map[string]spec{
	"ls":    {Class: Read, Implicit: true},
	"cat":   {Class: Read},
	"head":  {Class: Read},
	"tail":  {Class: Read},
	"wc":    {Class: Read},
	"stat":  {Class: Read},
	"file":  {Class: Read},
	"diff":  {Class: Read},
	"grep":  {Class: Read, CwdToo: true},
	"find":  {Class: Read, Implicit: true},
	"mkdir": {Class: Write},
	"touch": {Class: Write},
	"cp":    {Class: Read | Write},
	"mv":    {Class: Write | Delete},
	"rm":    {Class: Delete},
	"rmdir": {Class: Delete},
	"echo":  {NoPaths: true},
	"pwd":   {NoPaths: true},
	"true":  {NoPaths: true},
	"false": {NoPaths: true},
	"curl":  {Class: Network | Exec, NoPaths: true},
	"wget":  {Class: Network | Exec, NoPaths: true},
}

// ---------------------------------------------------------------- entry point

// classifyCommand returns one Action per simple command in cmd.
// An error means "can't analyze this", the caller should deny.
func ClassifyCommand(cmd, startDir string) ([]Action, error) {
	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	f, err := parser.Parse(strings.NewReader(cmd), "")
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if len(f.Stmts) == 0 {
		return nil, errors.New("empty command")
	}

	dir, err := resolvePath(startDir, ".")
	if err != nil {
		return nil, err
	}
	var all []Action
	for _, st := range f.Stmts { // top-level ';' and newlines run in order
		acts, newDir, err := classifyStmt(st, dir)
		if err != nil {
			return nil, err
		}
		all = append(all, acts...)
		dir = newDir
	}
	return all, nil
}

// ---------------------------------------------------------------- statements

// classifyStmt accepts: simple commands, &&, ||, | and |&, redirects.
// Everything else (subshells, blocks, if/for/while, functions, [[ ]], $(( ))...)
// is rejected by default.
func classifyStmt(st *syntax.Stmt, dir string) ([]Action, string, error) {
	if st.Background || st.Coprocess {
		return nil, dir, errors.New("background commands are not supported")
	}
	rclass, rpaths, err := classifyRedirs(st.Redirs, dir)
	if err != nil {
		return nil, dir, err
	}

	switch c := st.Cmd.(type) {
	case nil: // only redirects, like `> file`
		if rclass == 0 && len(rpaths) == 0 {
			return nil, dir, nil
		}
		return []Action{{Dir: dir, Class: rclass, Paths: rpaths}}, dir, nil

	case *syntax.CallExpr:
		act, newDir, err := classifyCall(c, dir)
		if err != nil {
			return nil, dir, err
		}
		act.Class |= rclass
		act.Paths = append(act.Paths, rpaths...)
		return []Action{act}, newDir, nil

	case *syntax.BinaryCmd:
		if len(st.Redirs) > 0 {
			return nil, dir, errors.New("redirect on a compound command is not supported")
		}
		switch c.Op {
		case syntax.AndStmt, syntax.OrStmt:
			left, d1, err := classifyStmt(c.X, dir)
			if err != nil {
				return nil, dir, err
			}
			right, d2, err := classifyStmt(c.Y, d1)
			if err != nil {
				return nil, dir, err
			}
			if c.Op == syntax.OrStmt && d2 != d1 {
				return nil, dir, errors.New("cd on the right side of || is not supported")
			}
			return append(left, right...), d2, nil

		case syntax.Pipe, syntax.PipeAll:
			// every stage is its own process, a cd in one doesn't carry over
			left, _, err := classifyStmt(c.X, dir)
			if err != nil {
				return nil, dir, err
			}
			right, _, err := classifyStmt(c.Y, dir)
			if err != nil {
				return nil, dir, err
			}
			return append(left, right...), dir, nil
		}
		return nil, dir, fmt.Errorf("unsupported operator %v", c.Op)
	}
	return nil, dir, fmt.Errorf("unsupported construct %T", st.Cmd)
}

// ---------------------------------------------------------------- simple command

func classifyCall(c *syntax.CallExpr, dir string) (Action, string, error) {
	act := Action{Dir: dir}

	// FOO=bar cmd can change behavior (PATH, LD_PRELOAD...), so no assignments
	if len(c.Assigns) > 0 {
		return act, dir, errors.New("variable assignments are not supported")
	}
	if len(c.Args) == 0 {
		return act, dir, errors.New("empty command")
	}

	words := make([]word, len(c.Args))
	act.Argv = make([]string, len(c.Args))
	for i, a := range c.Args {
		w, err := evalWord(a)
		if err != nil {
			return act, dir, err
		}
		words[i] = w
		act.Argv[i] = w.Text
	}
	name := act.Argv[0]
	if words[0].GlobAt >= 0 {
		return act, dir, errors.New("glob in command name")
	}

	// cd: the only command that changes our tracked dir
	if name == "cd" {
		if len(words) != 2 || strings.HasPrefix(act.Argv[1], "-") || words[1].GlobAt >= 0 {
			return act, dir, errors.New("cd needs exactly one plain path")
		}
		target, err := resolvePath(dir, act.Argv[1])
		if err != nil {
			return act, dir, err
		}
		if fi, err := os.Stat(target); err != nil || !fi.IsDir() {
			return act, dir, fmt.Errorf("cd target %q is not an existing directory", target)
		}
		act.Class = Read
		act.Paths = []Access{{Path: target, Mode: Read}}
		return act, target, nil
	}

	// ./script or /usr/bin/x: we match on the typed name, so a path is never "ls"
	if strings.Contains(name, "/") {
		p, err := resolvePath(dir, name)
		if err != nil {
			return act, dir, err
		}
		act.Class = Exec
		act.Paths = []Access{{Path: p, Mode: Read}}
		return act, dir, nil
	}

	sp, ok := specs[name]
	if !ok {
		act.Class = Exec
		return act, dir, nil
	}
	act.Class = sp.Class
	if sp.NoPaths {
		return act, dir, nil
	}

	var pathWords []word
	args := words[1:]
	if name == "find" {
		starts, expr := splitFind(args)
		pathWords = starts
		for _, w := range expr {
			switch w.Text {
			case "-delete":
				act.Class |= Delete
			case "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprint0", "-fprintf", "-fls":
				act.Class |= Exec
			}
		}
	} else {
		var err error
		pathWords, err = pathArgs(args)
		if err != nil {
			return act, dir, err
		}
	}

	mode := act.Class &^ (Network | Exec)
	for _, w := range pathWords {
		p, err := wordPath(dir, w)
		if err != nil {
			return act, dir, err
		}
		act.Paths = append(act.Paths, Access{Path: p, Mode: mode})
	}
	if sp.CwdToo || (sp.Implicit && len(pathWords) == 0) {
		act.Paths = append(act.Paths, Access{Path: dir, Mode: mode})
	}
	return act, dir, nil
}

// pathArgs treats every non-flag argument as a path. This over-counts
// (`head -n 5 f` makes "5" a path) but never drops a real path.
func pathArgs(args []word) ([]word, error) {
	var out []word
	afterDD := false
	for _, a := range args {
		if !afterDD && a.Text == "--" {
			afterDD = true
			continue
		}
		if !afterDD && strings.HasPrefix(a.Text, "-") && len(a.Text) > 1 {
			if strings.Contains(a.Text, "/") {
				return nil, fmt.Errorf("flag %q contains a path, pass it as a separate argument", a.Text)
			}
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

// splitFind separates the start paths from the expression.
func splitFind(args []word) (starts, expr []word) {
	i := 0
	for i < len(args) && (args[i].Text == "-H" || args[i].Text == "-L" || args[i].Text == "-P") {
		i++
	}
	for i < len(args) {
		t := args[i].Text
		if strings.HasPrefix(t, "-") || t == "(" || t == ")" || t == "!" || t == "," {
			break
		}
		starts = append(starts, args[i])
		i++
	}
	return starts, args[i:]
}

// ---------------------------------------------------------------- redirects

func classifyRedirs(rs []*syntax.Redirect, dir string) (Class, []Access, error) {
	var class Class
	var paths []Access
	for _, r := range rs {
		var mode Class
		switch r.Op {
		case syntax.RdrIn:
			mode = Read
		case syntax.RdrOut, syntax.AppOut, syntax.ClbOut, syntax.RdrAll, syntax.AppAll:
			mode = Write
		case syntax.RdrInOut:
			mode = Read | Write
		case syntax.DplIn, syntax.DplOut:
			w, err := evalWord(r.Word)
			if err != nil {
				return 0, nil, err
			}
			if isFD(w.Text) { // 2>&1, >&-
				continue
			}
			mode = Write // >&file
		default: // heredocs and here-strings
			return 0, nil, errors.New("heredocs and here-strings are not supported, use redirects (>, >>) with plain commands instead")
		}

		w, err := evalWord(r.Word)
		if err != nil {
			return 0, nil, err
		}
		p, err := wordPath(dir, w)
		if err != nil {
			return 0, nil, err
		}
		if w.GlobAt >= 0 {
			return 0, nil, errors.New("glob in redirect target")
		}
		if p == "/dev/null" { // 2>/dev/null is everywhere, not worth a prompt
			continue
		}
		class |= mode
		paths = append(paths, Access{Path: p, Mode: mode})
	}
	return class, paths, nil
}

func isFD(s string) bool {
	if s == "-" {
		return true
	}
	if s == "" {
		return false
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------- words

// word is one shell word after quote removal.
type word struct {
	Text   string
	GlobAt int // index in Text of the first unquoted * ? [ , or -1
}

// evalWord turns a parsed word into plain text. It only understands literals,
// quotes, and a leading ~ or ~/. Any other expansion ($x, $(..), `..`, $((..)),
// <(..), {a,b}, ~user, $'..') is an error: we can't know what it expands to.
func evalWord(w *syntax.Word) (word, error) {
	var sb strings.Builder
	globAt := -1

	for i, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit: // unquoted text, value is raw source (escapes still in it)
			s := p.Value
			if i == 0 && strings.HasPrefix(s, "~") {
				if s != "~" && !strings.HasPrefix(s, "~/") {
					return word{}, errors.New("~user is not supported")
				}
				if homeDir == "" {
					return word{}, errors.New("no home dir for ~")
				}
				sb.WriteString(homeDir)
				s = s[1:]
			}
			for j := 0; j < len(s); j++ {
				ch := s[j]
				switch {
				case ch == '\\' && j+1 < len(s): // escaped char is literal
					j++
					sb.WriteByte(s[j])
					continue
				case ch == '{' && !(j+1 < len(s) && s[j+1] == '}'): // {} is fine (find -exec)
					return word{}, errors.New("brace expansion is not supported")
				case (ch == '*' || ch == '?' || ch == '[') && globAt < 0:
					globAt = sb.Len()
				}
				sb.WriteByte(ch)
			}

		case *syntax.SglQuoted:
			if p.Dollar { // $'..' has escape sequences we don't decode
				return word{}, errors.New("$'..' strings are not supported")
			}
			sb.WriteString(p.Value)

		case *syntax.DblQuoted:
			if p.Dollar {
				return word{}, errors.New(`$".." strings are not supported`)
			}
			for _, q := range p.Parts {
				lit, ok := q.(*syntax.Lit)
				if !ok { // $var, $(..), `..` inside quotes
					return word{}, fmt.Errorf("unsupported expansion %T", q)
				}
				s := lit.Value
				for j := 0; j < len(s); j++ {
					// inside "..", backslash only escapes $ ` " \
					if s[j] == '\\' && j+1 < len(s) && strings.IndexByte("$`\"\\", s[j+1]) >= 0 {
						j++
					}
					sb.WriteByte(s[j])
				}
			}

		default:
			return word{}, fmt.Errorf("unsupported expansion %T", part)
		}
	}
	return word{Text: sb.String(), GlobAt: globAt}, nil
}

// ---------------------------------------------------------------- paths

// wordPath resolves a word to the path the policy should look at.
// For a glob it returns the directory before the first glob char: every
// match lives under it. (rm build/*.o -> build, rm * -> cwd)
func wordPath(dir string, w word) (string, error) {
	if w.GlobAt < 0 {
		return resolvePath(dir, w.Text)
	}
	if strings.Contains(w.Text[w.GlobAt:], "..") {
		return "", errors.New("glob containing .. is not supported")
	}
	prefix := w.Text[:w.GlobAt]
	base := "."
	if i := strings.LastIndex(prefix, "/"); i == 0 {
		base = "/"
	} else if i > 0 {
		base = prefix[:i]
	}
	return resolvePath(dir, base)
}

// resolvePath makes p absolute and resolves symlinks. EvalSymlinks fails on a
// path that doesn't exist yet (touch new.txt), so it resolves the deepest part
// that does exist and appends the rest as-is.
//
// If EvalSymlinks fails but the entry DOES exist (Lstat works), it is a
// dangling or looping symlink, or an unreadable dir. A write through a dangling
// link creates its target, which can be outside the root, so that is an error
// and the caller denies.
//
// Known gap: filepath.Clean collapses "link/.." lexically, the kernel follows
// the symlink first. Fine for a draft, fix before trusting it.
func resolvePath(dir, p string) (string, error) {
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	p = filepath.Clean(p)

	cur, rest := p, ""
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(real, rest), nil
		}
		if _, err := os.Lstat(cur); err == nil {
			return "", fmt.Errorf("cannot resolve %q (dangling or looping symlink?)", cur)
		}
		parent := filepath.Dir(cur)
		if parent == cur { // reached "/" and even that failed, don't loop forever
			return "", fmt.Errorf("cannot resolve %q", p)
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
