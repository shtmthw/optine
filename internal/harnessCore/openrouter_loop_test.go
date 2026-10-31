package harnessCore

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattthew/optine/internal/harnessSession"
)

func TestOpenRouterLoopToolCallRoundTrip(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader(""))

	sess := testSession(t, harnessSession.OpenAICompatNative)
	sess.BeginRun()
	sess.AppendUser("hi")

	var reqCount int
	var gotAuth, gotTitle, sawTool bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		gotAuth = true
		if r.Header.Get("X-Title") == "optine" {
			gotTitle = true
		}

		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []map[string]any `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		for _, m := range req.Messages {
			if m["role"] == "tool" && m["tool_call_id"] == "call_1" {
				sawTool = true
			}
		}

		reqCount++
		w.Header().Set("Content-Type", "application/json")
		if reqCount == 1 {
			fmt.Fprint(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"not_a_real_tool","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1}}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1}}`)
	}))
	t.Cleanup(srv.Close)

	out, err := openAiToolLoop(context.Background(), reader, "test-model", sess, srv.URL, "secret")
	if err != nil {
		t.Fatalf("loop: %v", err)
	}
	if out != "done" {
		t.Fatalf("want answer %q, got %q", "done", out)
	}
	if !gotAuth {
		t.Fatal("authorization header missing or wrong")
	}
	if !gotTitle {
		t.Fatal("X-Title header missing")
	}
	if !sawTool {
		t.Fatal("tool result not round-tripped to provider")
	}
}
