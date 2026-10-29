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

	"github.com/mattthew/optine/internal/dataTypes"
	"github.com/mattthew/optine/internal/harnessPermissions"
	"github.com/mattthew/optine/internal/harnessTools"
)

var ErrToolCallRejection = errors.New("the tool call request has been rejected")

// allowList remembers tools approved with "always" for this session.
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
			return dispatchWebSearch(ctx, call)

		case harnessPermissions.Remember:
			allowList["web_search"] = struct{}{}
			return dispatchWebSearch(ctx, call)

		default:
			return "User has rejected the tool call request.", ErrToolCallRejection
		}
	case "read_file":
		// Validate the range before prompting.
		startLine, endLine, err := harnessTools.ReadFileArgs(call.Arguments)
		if err != nil {
			log.Println(err)
			return "", err
		}

		realPath, err := harnessPermissions.ReadFilePolicy(call.Arguments, reader)

		if err != nil {
			log.Println(err)
			return "", err
		}
		return harnessTools.ReadFile(realPath, startLine, endLine)

	case "edit_file":
		// Policy resolves and approves the target; execution performs one
		// exact replacement (or a byte-for-byte append).
		doAppend := harnessPermissions.EditAppendFlag(call.Arguments)
		realPath, oldString, newString, err := harnessPermissions.EditFilePolicy(call.Arguments, reader, doAppend)
		if err != nil {
			log.Println(err)
			auditFileTool("edit_file", call.Arguments, "", "", "deny", fileDenyBy(err), err.Error())
			return "", err
		}
		info, err := harnessTools.EditFile(realPath, oldString, newString, doAppend)
		if err != nil {
			log.Println(err)
			auditFileTool("edit_file", call.Arguments, realPath, filepath.Dir(realPath), "deny", "policy", err.Error())
			return "", err
		}
		logFileToolResult("edit_file", realPath, info)
		auditFileTool("edit_file", call.Arguments, realPath, filepath.Dir(realPath), "exit_after_execution", "", "")
		return info, nil

	case "write_file":
		// Policy resolves and approves a create-only target.
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
		auditFileTool("write_file", call.Arguments, realPath, filepath.Dir(realPath), "exit_after_execution", "", "")
		return info, nil

	case "bash":
		// The bash tool takes json.RawMessage, so re-encode the dispatcher's
		// map. Denials are returned as tool text, not errors.
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

// auditFileTool appends one JSONL record per file-tool decision.
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

// logFileToolResult logs the tool result, hiding sensitive file contents.
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

	crawlerName, ok := call.Arguments["crawler"].(string)

	if !ok {
		return "", fmt.Errorf(
			"web_search: missing crawler argument",
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

	return harnessTools.SearchWeb(ctx, crawlerName, query)
}
