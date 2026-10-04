package harnessTools

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mattthew/optine/internals/harnessPermissions"
)

var (
	BashMu      sync.Mutex // one bash call at a time, grants and the audit log stay consistent
	SessionOnce sync.Once
	TheSession  *harnessPermissions.BashSession
	SessionErr  error
)

// BashTool is the whole public surface: pass it the tool call's arguments JSON.
// The workspace root is resolved here, on first use, from the directory the
// program was started in. There is no root argument and nothing for the model
// to change.
func BashToolCall(raw json.RawMessage, reader *bufio.Reader) harnessPermissions.BashOutcome {
	BashMu.Lock()
	defer BashMu.Unlock()

	SessionOnce.Do(func() {
		audit, err := harnessPermissions.DefaultAuditPath()
		if err != nil {
			SessionErr = err
			return
		}
		TheSession, SessionErr = harnessPermissions.NewBashSession("", audit)
	})
	if SessionErr != nil {
		return harnessPermissions.BashOutcome{Text: "bash is unavailable: " + SessionErr.Error()}
	}
	return bashTool(TheSession, raw, reader)
}

func bashTool(s *harnessPermissions.BashSession, raw json.RawMessage, reader *bufio.Reader) harnessPermissions.BashOutcome {
	args, err := decodeBashArgs(raw)
	if err != nil {
		return harnessPermissions.BashOutcome{Text: fmt.Sprintf(
			`invalid arguments: %v. expected {"command": string, "description": string (optional), "timeout_ms": int (optional)}`, err)}
	}

	// 1. classify. An error means "can't analyze", which is a deny.
	actions, err := harnessPermissions.ClassifyCommand(args.Command, s.Root)
	if err != nil {
		harnessPermissions.WriteAudit(s.AuditPath, harnessPermissions.AuditRecord{Command: args.Command, Dir: s.Root, Decision: "deny", By: "policy", Reason: err.Error()})
		return harnessPermissions.BashOutcome{
			Text:         "denied: " + err.Error() + ". Rewrite it as simple commands (no $(...), variables, loops, subshells, heredocs).",
			PolicyDenied: true,
		}
	}

	// 2. decide
	decision, why := harnessPermissions.BashPolicy(actions, s.Root, s.Grants)
	by := "rule"
	switch decision {
	case harnessPermissions.Deny:
		harnessPermissions.WriteAudit(s.AuditPath, harnessPermissions.AuditRecord{Command: args.Command, Dir: s.Root, Actions: actions, Decision: "deny", By: "policy", Reason: why})
		return harnessPermissions.BashOutcome{Text: "denied: " + why, PolicyDenied: true}

	case harnessPermissions.Ask:
		by = "user"
		ans, err := harnessPermissions.AskFunc("bash", reader, harnessPermissions.BashArgsMap(args), harnessPermissions.AskContent(actions, why))
		if err != nil { // closed stdin, read error...: fail closed
			harnessPermissions.WriteAudit(s.AuditPath, harnessPermissions.AuditRecord{Command: args.Command, Dir: s.Root, Actions: actions, Decision: "deny", By: "user", Reason: "approval prompt failed: " + err.Error()})
			return harnessPermissions.BashOutcome{Text: "not run: the approval prompt failed: " + err.Error(), UserDenied: true}
		}
		switch ans {
		case harnessPermissions.Once:
			// run this call, ask again next time
		case harnessPermissions.Remember:
			// AskFunc's label says "this tool", but granting the whole bash tool
			// would turn every later ask (rm -rf, outside the workspace, curl)
			// into an allow. So for bash it means: these exact commands, in
			// these exact dirs. Deny and unanalyzable commands are never grantable.
			for _, a := range actions {
				if d, _ := harnessPermissions.PolicyForAction(a, s.Root); d == harnessPermissions.Ask && len(a.Argv) > 0 {
					s.Grants = append(s.Grants, harnessPermissions.Grant{Argv: strings.Join(a.Argv, "\x00"), Dir: a.Dir})
				}
			}
		default: // No, or any value we don't know
			harnessPermissions.WriteAudit(s.AuditPath, harnessPermissions.AuditRecord{Command: args.Command, Dir: s.Root, Actions: actions, Decision: "deny", By: "user", Reason: why})
			return harnessPermissions.BashOutcome{Text: "the user denied this command: " + why, UserDenied: true}
		}
	}

	// 3. log BEFORE running. If the log can't be written, don't run.
	rec := harnessPermissions.AuditRecord{Command: args.Command, Dir: s.Root, Actions: actions, Decision: "allow", By: by, Reason: why}
	if err := harnessPermissions.WriteAudit(s.AuditPath, rec); err != nil {
		return harnessPermissions.BashOutcome{Text: "not run: audit log unavailable: " + err.Error()}
	}

	// 4. run exactly the string that was classified
	timeout := harnessPermissions.DefaultTimeout
	if args.TimeoutMs > 0 {
		timeout = min(time.Duration(args.TimeoutMs)*time.Millisecond, harnessPermissions.MaxTimeout)
	}
	text, code := harnessPermissions.ExecBash(args.Command, s.Root, timeout)

	harnessPermissions.WriteAudit(s.AuditPath, harnessPermissions.AuditRecord{Command: args.Command, Dir: s.Root, Decision: "exit", ExitCode: &code})
	return harnessPermissions.BashOutcome{Text: text}
}

// decodeBashArgs parses the tool call arguments.
func decodeBashArgs(raw json.RawMessage) (harnessPermissions.BashArgs, error) {
	var a harnessPermissions.BashArgs
	// some servers send "arguments" as a string that contains the JSON object
	if len(raw) > 0 && raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return a, err
		}
		raw = json.RawMessage(s)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields() // a model-supplied "workdir" is an error, not silently ignored
	if err := dec.Decode(&a); err != nil {
		return a, err
	}
	if strings.TrimSpace(a.Command) == "" {
		return a, errors.New("command is required")
	}
	if len(a.Command) > harnessPermissions.MaxCommandLen {
		return a, errors.New("command too long")
	}
	return a, nil
}
