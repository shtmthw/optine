package harnessDispatch

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/mattthew/optine/internals/dataTypes"
	"github.com/mattthew/optine/internals/harnessPermissions"
	"github.com/mattthew/optine/internals/harnessTools"
)

var ErrToolCallRejection = errors.New("the tool call request has been rejected")

// allowList remembers tools the user approved with "always" for this session.
// Answers are harnessPermissions.Answer values (No/Once/Remember).
var allowList = make(map[string]struct{})

func Dispatch(ctx context.Context, reader *bufio.Reader, call *dataTypes.NativeLLMResponse) (string, error) {
	switch call.Tool {
	case "web_search":
		if _, allowed := allowList["web_search"]; allowed {
			return dispatchWebSearch(ctx, call)
		}

		resp, err := harnessPermissions.AskFunc(call.Tool, reader, call.Arguments, call.Content)
		if err != nil {
			log.Println("error occurred whilst running Ask() in the dispatch:", err)
			return "", err
		}

		switch resp {
		case harnessPermissions.Once:
			// allow once
			return dispatchWebSearch(ctx, call)

		case harnessPermissions.Remember:
			// always allow this tool
			allowList["web_search"] = struct{}{}
			return dispatchWebSearch(ctx, call)

		default:
			return "User has rejected the tool call request.", ErrToolCallRejection
		}
	case "read_file":

		realPath, err := harnessPermissions.ReadFilePolicy(call.Arguments, reader)

		if err != nil {
			log.Println(err)
			return "", err
		}
		return harnessTools.ReadFile(realPath)

	case "edit_file":
		// Policy resolves and approves the path (cwd pre-approved, other
		// dirs ask once, sensitive/symlink/hardlink always ask); execution
		// does one exact old->new replacement and returns a deterministic
		// harness reply built from what was actually read and written.
		realPath, oldString, newString, err := harnessPermissions.EditFilePolicy(call.Arguments, reader)
		if err != nil {
			log.Println(err)
			auditFileTool("edit_file", call.Arguments, "", "", "deny", fileDenyBy(err), err.Error())
			return "", err
		}
		info, err := harnessTools.EditFile(realPath, oldString, newString)
		if err != nil {
			log.Println(err)
			auditFileTool("edit_file", call.Arguments, realPath, filepath.Dir(realPath), "deny", "policy", err.Error())
			return "", err
		}
		logFileToolResult("edit_file", realPath, info)
		auditFileTool("edit_file", call.Arguments, realPath, filepath.Dir(realPath), "exit", "", "")
		return info, nil

	case "write_file":
		// Policy resolves and approves the target (create-only, symlinks
		// are a hard deny, sensitive always asks); execution creates parents
		// and writes atomically, returning a deterministic harness reply.
		realPath, content, err := harnessPermissions.WriteFilePolicy(call.Arguments, reader)
		if err != nil {
			log.Println(err)
			auditFileTool("write_file", call.Arguments, "", "", "deny", fileDenyBy(err), err.Error())
			return "", err
		}
		info, err := harnessTools.WriteFile(realPath, content)
		if err != nil {
			log.Println(err)
			auditFileTool("write_file", call.Arguments, realPath, filepath.Dir(realPath), "deny", "policy", err.Error())
			return "", err
		}
		logFileToolResult("write_file", realPath, info)
		auditFileTool("write_file", call.Arguments, realPath, filepath.Dir(realPath), "exit", "", "")
		return info, nil

	case "bash":
		// The dispatcher speaks map[string]any; the bash tool speaks
		// json.RawMessage (see decodeBashArgs). Re-encode here so both
		// native (Ollama map, vLLM JSON string -> map) and non-native
		// envelope paths funnel through the same classify -> policy ->
		// ask -> audit -> exec pipeline. BashOutcome.Text already holds
		// the denied / not-run / output text for the model, so a denial
		// is a successful dispatch with explanatory text, not an error.
		raw, err := json.Marshal(call.Arguments)
		if err != nil {
			return "", fmt.Errorf("bash: encoding arguments: %w", err)
		}
		outcome := harnessTools.BashToolCall(raw, reader)
		return outcome.Text, nil

	default:
		return "", fmt.Errorf(
			"unknown tool call: %q",
			call.Tool,
		)
	}
}

// fileDenyBy mirrors the bash audit's by field: user denials vs policy errors.
func fileDenyBy(err error) string {
	if errors.Is(err, harnessPermissions.ErrEditDenied) || errors.Is(err, harnessPermissions.ErrWriteDenied) {
		return "user"
	}
	return "policy"
}

// auditFileTool appends one JSONL record per edit_file/write_file decision to
// the same audit file bash uses. Best-effort only: unlike bash (fail-closed
// before exec), the mutation or denial is already decided here, so an
// unwritable log is reported, not fatal.
func auditFileTool(tool string, args map[string]any, realPath, dir, decision, by, reason string) {
	auditPath, err := harnessPermissions.DefaultAuditPath()
	if err != nil {
		log.Println("audit unavailable:", err)
		return
	}
	cmd := tool
	switch {
	case realPath != "":
		cmd += " " + realPath
	case args["path"] != nil:
		if p, ok := args["path"].(string); ok && p != "" {
			cmd += " " + p
		}
	}
	if err := harnessPermissions.WriteAudit(auditPath, harnessPermissions.AuditRecord{
		Command:  cmd,
		Dir:      dir,
		Decision: decision,
		By:       by,
		Reason:   reason,
	}); err != nil {
		log.Println("audit unavailable:", err)
	}
}

// logFileToolResult feeds the deterministic harness reply to the TUI event
// feed. Sensitive files log metadata only so secrets never reach logs; the
// model still receives the full text as the tool result.
func logFileToolResult(tool, realPath, info string) {
	if harnessPermissions.IsSensitive(realPath) {
		log.Printf("%s %s: applied (content hidden: sensitive file)", tool, realPath)
		return
	}
	log.Println(info)
}

func dispatchWebSearch(ctx context.Context, call *dataTypes.NativeLLMResponse) (string, error) {
	rawQuery, ok := call.Arguments["query"]

	if !ok {
		return "", fmt.Errorf(
			"web_search: missing query argument",
		)
	}

	query, ok := rawQuery.(string)
	if !ok {
		return "", fmt.Errorf(
			"web_search: query must be a string, got %T",
			rawQuery,
		)
	}

	query = strings.TrimSpace(query)

	if query == "" {
		return "", fmt.Errorf(
			"web_search: query cannot be empty",
		)
	}

	if len(query) > 1000 {
		return "", fmt.Errorf(
			"web_search: query is too long",
		)
	}

	return harnessTools.SearchWeb(ctx, query)
}
