// bash tool for Casual Mode:
//
//	LLM tool call JSON → decodeBashArgs → classifyCommand → bashPolicy
//	  → (AskFunc) → audit → execBash → tool result text for the model
//
// Uses the package's own AskFunc / Answer (ask.go). No methods.
// Linux/macOS only (process groups via syscall).
package harnessPermissions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	MaxOutput      = 64 * 1024
	MaxCommandLen  = 8 * 1024
	DefaultTimeout = 30 * time.Second
	MaxTimeout     = 120 * time.Second
)

type Decision int

const (
	Allow Decision = iota // ordered: the strictest decision across actions wins
	Ask
	Deny
)

// Grant is what Remember saves for bash: this exact argv, in this exact dir.
// It is NOT "allow the bash tool", see the note in bashTool.
type Grant struct {
	Argv string // argv joined with NUL
	Dir  string
}

type BashSession struct {
	Root      string // resolved workspace root, set once, never from the model
	Grants    []Grant
	AuditPath string // JSONL file
}

// BashArgs is what the model sends. There is deliberately no "workdir".
type BashArgs struct {
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
	TimeoutMs   int    `json:"timeout_ms,omitempty"`
}

// BashOutcome is what the agent loop gets back.
type BashOutcome struct {
	Text         string // goes back to the model as the tool result
	PolicyDenied bool   // the command itself was rejected
	UserDenied   bool   // the user said no, or the prompt failed (stronger stop signal)
}

func DefaultAuditPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "optine")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "audit.jsonl"), nil
}

// newBashSession sets Root: from rootArg, or from the current directory when
// rootArg is "". Called once (by BashTool), never from the model.
func NewBashSession(rootArg, auditPath string) (*BashSession, error) {
	if rootArg == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		rootArg = wd
	}
	root, err := resolvePath(rootArg, ".")
	if err != nil {
		return nil, err
	}

	// ~ and the workspace are different things. If home is inside the root
	// (root is "/", "/home" or home itself) everything in home counts as "inside".
	home, err := resolvePath(homeDir, ".")
	if err != nil {
		return nil, err
	}
	if InsideRoot(root, home) {
		return nil, fmt.Errorf("workspace root %q contains your home dir, pick a project directory", root)
	}
	return &BashSession{Root: root, AuditPath: auditPath}, nil
}

// ---------------------------------------------------------------- policy

// bashPolicy returns the strictest decision over all actions.
func BashPolicy(actions []Action, root string, grants []Grant) (Decision, string) {
	worst, reason := Allow, ""
	for _, a := range actions {
		d, why := PolicyForAction(a, root)
		if d == Ask && HasGrant(grants, a) { // only Ask can be granted, never Deny
			d = Allow
		}
		if d > worst {
			worst, reason = d, why
		}
	}
	return worst, reason
}

func PolicyForAction(a Action, root string) (Decision, string) {
	// deny first, across every path
	for _, p := range a.Paths {
		if IsSensitive(p.Path) {
			return Deny, "touches a sensitive path: " + p.Path
		}
	}
	if !InsideRoot(root, a.Dir) {
		return Ask, "runs in " + a.Dir + ", outside the workspace"
	}
	for _, p := range a.Paths {
		if !InsideRoot(root, p.Path) {
			return Ask, "touches " + p.Path + ", outside the workspace"
		}
	}
	if a.Class&Delete != 0 {
		return Ask, "deletes or overwrites files"
	}
	if a.Class&Network != 0 {
		return Ask, "uses the network"
	}
	if a.Class&Exec != 0 {
		return Ask, "runs a program whose effects can't be checked"
	}
	return Allow, "" // reads and writes inside the workspace
}

func HasGrant(grants []Grant, a Action) bool {
	if len(a.Argv) == 0 {
		return false
	}
	key := strings.Join(a.Argv, "\x00")
	for _, g := range grants {
		if g.Argv == key && g.Dir == a.Dir {
			return true
		}
	}
	return false
}

// insideRoot: root and p must both be resolved already.
func InsideRoot(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// isSensitive is a small deny list. In Casual Mode it only covers what the
// classifier can see, python -c "open('~/.ssh/id_rsa')" still gets past it.
func IsSensitive(p string) bool {
	for _, elem := range strings.Split(p, string(filepath.Separator)) {
		switch elem {
		case ".ssh", ".gnupg", ".password-store":
			return true
		}
		if strings.HasPrefix(elem, ".env") {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- what AskFunc shows

// bashArgsMap is the "arguments" AskFunc prints. The model's own description
// is in here, so the user sees it as an argument the model sent.
func BashArgsMap(args BashArgs) map[string]any {
	m := map[string]any{"command": args.Command}
	if args.Description != "" {
		m["description"] = args.Description
	}
	if args.TimeoutMs > 0 {
		m["timeout_ms"] = args.TimeoutMs
	}
	return m
}

// askContent is our analysis, the `content` AskFunc prints: why we are asking,
// and every command with the resolved absolute paths it touches.
func AskContent(actions []Action, why string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "asked because it %s\n", why)
	for _, a := range actions {
		fmt.Fprintf(&sb, "    in %s: %s  [%s]\n", a.Dir, strings.Join(a.Argv, " "), ClassNames(a.Class))
		for _, p := range a.Paths {
			fmt.Fprintf(&sb, "        %s  (%s)\n", p.Path, ClassNames(p.Mode))
		}
	}
	return sb.String()
}

func ClassNames(c Class) string {
	var names []string
	for _, n := range []struct {
		c Class
		s string
	}{{Read, "read"}, {Write, "write"}, {Delete, "delete"}, {Network, "network"}, {Exec, "exec"}} {
		if c&n.c != 0 {
			names = append(names, n.s)
		}
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, "+")
}

// ---------------------------------------------------------------- audit

type AuditRecord struct {
	Time     string   `json:"time"`
	Command  string   `json:"command"`
	Dir      string   `json:"dir"`
	Actions  []Action `json:"actions,omitempty"`
	Decision string   `json:"decision"` // allow | deny | exit
	By       string   `json:"by,omitempty"`
	Reason   string   `json:"reason,omitempty"`
	ExitCode *int     `json:"exit_code,omitempty"`
}

func WriteAudit(path string, rec AuditRecord) error {
	rec.Time = time.Now().UTC().Format(time.RFC3339Nano)
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// ---------------------------------------------------------------- execution

// execBash runs the command with bash -c (the same dialect the classifier
// parsed), with a scrubbed env, no stdin, a timeout, and capped output.
func ExecBash(command, dir string, timeout time.Duration) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = dir
	cmd.Env = CleanEnv()
	cmd.Stdin = nil // /dev/null
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }

	// one pipe for stdout+stderr so we can stop reading at the cap
	pr, pw, err := os.Pipe()
	if err != nil {
		return "failed to start: " + err.Error(), -1
	}
	defer pr.Close()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		return "failed to start: " + err.Error(), -1
	}
	pw.Close()

	// A background process that escapes the process group can keep this pipe
	// open past the timeout. Only a real sandbox fixes that.
	buf, _ := io.ReadAll(io.LimitReader(pr, MaxOutput+1))
	truncated := len(buf) > MaxOutput
	if truncated {
		buf = buf[:MaxOutput]
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	waitErr := cmd.Wait()

	code := 0
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}

	text := string(buf)
	if truncated {
		text += fmt.Sprintf("\n[output truncated at %d bytes, command killed]", MaxOutput)
	}
	if ctx.Err() == context.DeadlineExceeded {
		text += fmt.Sprintf("\n[timed out after %s]", timeout)
	}
	return fmt.Sprintf("%s\n[exit code %d]", text, code), code
}

// cleanEnv passes only what a normal command needs, so API keys and tokens in
// the harness's environment don't reach the model's commands. HOME stays real
// so bash expands ~ to the same path the classifier used.
func CleanEnv() []string {
	var env []string
	for _, k := range []string{"PATH", "HOME", "USER", "LANG", "LC_ALL", "TERM", "TMPDIR"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}
