package harnessDispatch

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/mattthew/optine/internals/dataTypes"
	"github.com/mattthew/optine/internals/harnessPermissions"
	"github.com/mattthew/optine/internals/harnessTools"
)

var ErrToolCallRejection = errors.New("the tool call request has been rejected")

// rules:
// 0 is DENIED
// 1 is Allow ONCE
// 2 is Allow ALWAYS

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
		case 1:
			// allow once
			return dispatchWebSearch(ctx, call)

		case 2:
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
