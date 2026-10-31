package harnessCore

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mattthew/optine/internal/harnessSession"
)

// dynamicToolRounds is deliberately above baseTurns: reaching the final answer
// proves the loop grew its starting budget instead of stopping at ten.
const dynamicToolRounds = baseTurns + 2

const unknownTool = "not_a_real_tool"

// scriptedJSONServer answers each request with body(call), where call is
// 1-based. It points one endpoint var at the server and restores it after.
func scriptedJSONServer(t *testing.T, urlVar *string, body func(call int64) string) *int64 {
	t.Helper()

	var calls int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body(n))
	}))
	t.Cleanup(srv.Close)

	old := *urlVar
	*urlVar = srv.URL
	t.Cleanup(func() { *urlVar = old })

	return &calls
}

// Unknown tool calls are safe: runTool rejects the name before dispatch, so
// the tests exercise turn accounting without touching the filesystem.

func ollamaNativeToolCall() string {
	return `{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","function":{"name":"` + unknownTool + `","arguments":{}}}]},"prompt_eval_count":1}`
}

func ollamaNativeFinalAnswer() string {
	return `{"message":{"role":"assistant","content":"done"},"prompt_eval_count":1}`
}

func vllmNativeToolCall() string {
	return `{"choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"` + unknownTool + `","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1}}`
}

func vllmNativeFinalAnswer() string {
	return `{"choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1}}`
}

const envelopeToolCall = `{"type":"tool_call","tool":"` + unknownTool + `","arguments":{}}`

const envelopeFinalAnswer = `{"type":"final_answer","content":"done"}`

func ollamaEnvelopeBody(content string) string {
	return fmt.Sprintf(`{"message":{"role":"assistant","content":%q},"prompt_eval_count":1}`, content)
}

func vllmEnvelopeBody(content string) string {
	return fmt.Sprintf(`{"choices":[{"index":0,"message":{"role":"assistant","content":%q}}],"usage":{"prompt_tokens":1}}`, content)
}

func TestOllamaLoopGrowsPastBaseTurns(t *testing.T) {
	calls := scriptedJSONServer(t, &ollamaChatURL, func(call int64) string {
		if call <= dynamicToolRounds {
			return ollamaNativeToolCall()
		}

		return ollamaNativeFinalAnswer()
	})

	reader := bufio.NewReader(strings.NewReader(""))

	out, err := ollamaToolLoop(context.Background(), reader, "test-model", testSession(t, harnessSession.OllamaNative))
	if err != nil {
		t.Fatalf("loop: %v", err)
	}

	if out != "done" {
		t.Fatalf("want answer %q, got %q", "done", out)
	}

	if got := atomic.LoadInt64(calls); got != dynamicToolRounds+1 {
		t.Fatalf("want %d provider calls, got %d", dynamicToolRounds+1, got)
	}
}

func TestVLLMLoopGrowsPastBaseTurns(t *testing.T) {
	calls := scriptedJSONServer(t, &vLLMChatURL, func(call int64) string {
		if call <= dynamicToolRounds {
			return vllmNativeToolCall()
		}

		return vllmNativeFinalAnswer()
	})

	reader := bufio.NewReader(strings.NewReader(""))

	out, err := openAiToolLoop(context.Background(), reader, "test-model", testSession(t, harnessSession.OpenAICompatNative), vLLMChatURL, "")
	if err != nil {
		t.Fatalf("loop: %v", err)
	}

	if out != "done" {
		t.Fatalf("want answer %q, got %q", "done", out)
	}

	if got := atomic.LoadInt64(calls); got != dynamicToolRounds+1 {
		t.Fatalf("want %d provider calls, got %d", dynamicToolRounds+1, got)
	}
}

func TestOllamaEnvelopeLoopGrowsPastBaseTurns(t *testing.T) {
	calls := scriptedJSONServer(t, &ollamaChatURL, func(call int64) string {
		if call <= dynamicToolRounds {
			return ollamaEnvelopeBody(envelopeToolCall)
		}

		return ollamaEnvelopeBody(envelopeFinalAnswer)
	})

	reader := bufio.NewReader(strings.NewReader(""))

	out, err := nonNativeAgentCall(context.Background(), testSession(t, harnessSession.Envelope), reader, "Ollama", "test-model")
	if err != nil {
		t.Fatalf("loop: %v", err)
	}

	if out != "done" {
		t.Fatalf("want answer %q, got %q", "done", out)
	}

	if got := atomic.LoadInt64(calls); got != dynamicToolRounds+1 {
		t.Fatalf("want %d provider calls, got %d", dynamicToolRounds+1, got)
	}
}

func TestVLLMEnvelopeLoopGrowsPastBaseTurns(t *testing.T) {
	calls := scriptedJSONServer(t, &vLLMChatURL, func(call int64) string {
		if call <= dynamicToolRounds {
			return vllmEnvelopeBody(envelopeToolCall)
		}

		return vllmEnvelopeBody(envelopeFinalAnswer)
	})

	reader := bufio.NewReader(strings.NewReader(""))

	out, err := nonNativeAgentCall(context.Background(), testSession(t, harnessSession.Envelope), reader, "vLLM", "test-model")
	if err != nil {
		t.Fatalf("loop: %v", err)
	}

	if out != "done" {
		t.Fatalf("want answer %q, got %q", "done", out)
	}

	if got := atomic.LoadInt64(calls); got != dynamicToolRounds+1 {
		t.Fatalf("want %d provider calls, got %d", dynamicToolRounds+1, got)
	}
}

func TestOllamaLoopStopsAtMaxTurns(t *testing.T) {
	calls := scriptedJSONServer(t, &ollamaChatURL, func(int64) string {
		return ollamaNativeToolCall()
	})

	reader := bufio.NewReader(strings.NewReader(""))

	_, err := ollamaToolLoop(context.Background(), reader, "test-model", testSession(t, harnessSession.OllamaNative))
	if !errors.Is(err, ErrMaxInferenceCalls) {
		t.Fatalf("want ErrMaxInferenceCalls, got: %v", err)
	}

	if got := atomic.LoadInt64(calls); got != MaxTurns {
		t.Fatalf("want %d provider calls, got %d", MaxTurns, got)
	}
}

func TestVLLMLoopStopsAtMaxTurns(t *testing.T) {
	calls := scriptedJSONServer(t, &vLLMChatURL, func(int64) string {
		return vllmNativeToolCall()
	})

	reader := bufio.NewReader(strings.NewReader(""))

	_, err := openAiToolLoop(context.Background(), reader, "test-model", testSession(t, harnessSession.OpenAICompatNative), vLLMChatURL, "")
	if !errors.Is(err, ErrMaxInferenceCalls) {
		t.Fatalf("want ErrMaxInferenceCalls, got: %v", err)
	}

	if got := atomic.LoadInt64(calls); got != MaxTurns {
		t.Fatalf("want %d provider calls, got %d", MaxTurns, got)
	}
}

func TestOllamaEnvelopeLoopStopsAtMaxTurns(t *testing.T) {
	calls := scriptedJSONServer(t, &ollamaChatURL, func(int64) string {
		return ollamaEnvelopeBody(envelopeToolCall)
	})

	reader := bufio.NewReader(strings.NewReader(""))

	_, err := nonNativeAgentCall(context.Background(), testSession(t, harnessSession.Envelope), reader, "Ollama", "test-model")
	if !errors.Is(err, ErrMaxInferenceCalls) {
		t.Fatalf("want ErrMaxInferenceCalls, got: %v", err)
	}

	if got := atomic.LoadInt64(calls); got != MaxTurns {
		t.Fatalf("want %d provider calls, got %d", MaxTurns, got)
	}
}

func TestVLLMEnvelopeLoopStopsAtMaxTurns(t *testing.T) {
	calls := scriptedJSONServer(t, &vLLMChatURL, func(int64) string {
		return vllmEnvelopeBody(envelopeToolCall)
	})

	reader := bufio.NewReader(strings.NewReader(""))

	_, err := nonNativeAgentCall(context.Background(), testSession(t, harnessSession.Envelope), reader, "vLLM", "test-model")
	if !errors.Is(err, ErrMaxInferenceCalls) {
		t.Fatalf("want ErrMaxInferenceCalls, got: %v", err)
	}

	if got := atomic.LoadInt64(calls); got != MaxTurns {
		t.Fatalf("want %d provider calls, got %d", MaxTurns, got)
	}
}
