package harnessDispatch

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/mattthew/optine/internals/dataTypes"
	"github.com/mattthew/optine/internals/harnessTools"
)

var ErrToolCallRejection = errors.New("the tool call request has been rejected")

// rules:
// 0 is DENIED
// 1 is Allow ONCE
// 2 is Allow ALWAYS

var allowList = make(map[string]struct{})

func Dispatch(ctx context.Context, reader *bufio.Reader, call *dataTypes.AIResponse) (string, error) {
	switch call.Tool {
	case "web_search":
		if _, allowed := allowList["web_search"]; allowed {
			return dispatchWebSearch(ctx, call)
		}

		resp, err := Ask(call.Tool, reader, call.Arguments, call.Content)
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

	default:
		return "", fmt.Errorf(
			"unknown tool call: %q",
			call.Tool,
		)
	}
}

func dispatchWebSearch(ctx context.Context, call *dataTypes.AIResponse) (string, error) {
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
