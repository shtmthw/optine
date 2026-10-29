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

	"github.com/mattthew/optine/internal/harnessPermissions"
)

var (
	BashMu      sync.Mutex // one bash call at a time, grants and the audit log stay consistent
	SessionOnce sync.Once
	TheSession  *harnessPermissions.BashSession
	SessionErr  error
)

// BashToolCall runs one bash tool call. The workspace root is resolved once
// from the starting directory; the model cannot change it.
func BashToolCall(raw json.RawMessage, reader *bufio.Reader) harnessPermissions.BashOutcome {
	BashMu.Lock()
	defer BashMu.Unlock()

	SessionOnce.Do(func() {
		auditPath, err := harnessPermissions.DefaultAuditPath()
		if err != nil {
			SessionErr = err
			return
		}
		TheSession, SessionErr = harnessPermissions.NewBashSession("", auditPath)
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

	// Classify; an error means the command cannot be analyzed and is denied.
	actions, err := harnessPermissions.ClassifyCommand(args.Command, s.Root)
	if err != nil {
		harnessPermissions.WriteAudit(s.AuditPath, harnessPermissions.AuditRecord{Command: args.Command, Dir: s.Root, Decision: "deny", By: "policy", Reason: err.Error()})
		return harnessPermissions.BashOutcome{
			Text: "denied: " + err.Error() + ". Rewrite it as simple commands (no $(...), variables, loops, subshells, heredocs).",
		}
	}

	decision, why := harnessPermissions.BashPolicy(actions, s.Root, s.Grants)
	by := "rule"
	switch decision {
	case harnessPermissions.Deny:
		harnessPermissions.WriteAudit(s.AuditPath, harnessPermissions.AuditRecord{Command: args.Command, Dir: s.Root, Actions: actions, Decision: "deny", By: "policy", Reason: why})
		return harnessPermissions.BashOutcome{Text: "denied: " + why}

	case harnessPermissions.Ask:
		by = "user"
		ans, err := harnessPermissions.AskFunc("bash", reader, harnessPermissions.BashArgsMap(args), harnessPermissions.AskContent(actions, why))
		if err != nil { // closed stdin, read error...: fail closed
			harnessPermissions.WriteAudit(s.AuditPath, harnessPermissions.AuditRecord{Command: args.Command, Dir: s.Root, Actions: actions, Decision: "deny", By: "user", Reason: "approval prompt failed: " + err.Error()})
			return harnessPermissions.BashOutcome{Text: "not run: the approval prompt failed: " + err.Error()}
		}
		switch ans {
		case harnessPermissions.Once:
		case harnessPermissions.Remember:
			// Remember grants these exact commands, not the whole bash tool.
			for _, a := range actions {
				if d, _ := harnessPermissions.PolicyForAction(a, s.Root); d == harnessPermissions.Ask && len(a.Argv) > 0 {
					s.Grants = append(s.Grants, harnessPermissions.Grant{Argv: strings.Join(a.Argv, "\x00"), Dir: a.Dir})
				}
			}
		default:
			harnessPermissions.WriteAudit(s.AuditPath, harnessPermissions.AuditRecord{Command: args.Command, Dir: s.Root, Actions: actions, Decision: "deny", By: "user", Reason: why})
			return harnessPermissions.BashOutcome{Text: "the user denied this command: " + why}
		}
	}

	// Log before running; if the audit write fails, do not run.
	rec := harnessPermissions.AuditRecord{Command: args.Command, Dir: s.Root, Actions: actions, Decision: "allow", By: by, Reason: why}
	if err := harnessPermissions.WriteAudit(s.AuditPath, rec); err != nil {
		return harnessPermissions.BashOutcome{Text: "not run: audit log unavailable: " + err.Error()}
	}

	// Run exactly the string that was classified.
	timeout := harnessPermissions.DefaultTimeout
	if args.TimeoutMs > 0 {
		timeout = min(time.Duration(args.TimeoutMs)*time.Millisecond, harnessPermissions.MaxTimeout)
	}
	text, code := harnessPermissions.ExecBash(args.Command, s.Root, timeout)

	harnessPermissions.WriteAudit(s.AuditPath, harnessPermissions.AuditRecord{Command: args.Command, Dir: s.Root, Decision: "exit_after_execution", ExitCode: &code})
	return harnessPermissions.BashOutcome{Text: text}
}

// decodeBashArgs parses the tool call arguments.
func decodeBashArgs(raw json.RawMessage) (harnessPermissions.BashArgs, error) {
	var a harnessPermissions.BashArgs
	// Some servers send arguments as a JSON string.
	if len(raw) > 0 && raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return a, err
		}
		raw = json.RawMessage(s)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields() // reject a model-supplied "workdir"
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
