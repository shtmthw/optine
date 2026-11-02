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

// testSession builds an in-memory session for the loops. Nothing here commits:
// Commit only runs inside AgentLoop, so these tests stay off disk.
func testSession(t *testing.T, kind harnessSession.Kind) *harnessSession.Session {
	t.Helper()

	return harnessSession.New(harnessSession.Config{
		Provider: "Ollama",
		Model:    "test-model",
		Kind:     kind,
		MaxTurns: MaxTurns,
		ToolCall: kind != harnessSession.Envelope,
	})
}

// garbageServer serves 200 with an unparseable body on every hit and counts
// the hits. It points one endpoint var at the server and restores it after.
func garbageServer(t *testing.T, urlVar *string) *int64 {
	t.Helper()
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, "not json {{{")
	}))
	t.Cleanup(srv.Close)
	old := *urlVar
	*urlVar = srv.URL
	t.Cleanup(func() { *urlVar = old })
	return &calls
}

func TestOllamaMalformedBodiesAbortAfterBudget(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader(""))
	calls := garbageServer(t, &ollamaChatURL)
	_, err := ollamaToolLoop(context.Background(), reader, "test-model", testSession(t, harnessSession.OllamaNative))
	if !errors.Is(err, ErrMalformedJSON) {
		t.Fatalf("want abort wrapping ErrMalformedJSON, got: %v", err)
	}
	if got := atomic.LoadInt64(calls); got != maxBadBodies+1 {
		t.Fatalf("want %d provider calls (1 + %d retries), got %d", maxBadBodies+1, maxBadBodies, got)
	}
}

func TestVLLMMalformedBodiesAbortAfterBudget(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader(""))
	calls := garbageServer(t, &vLLMChatURL)
	_, err := openAiToolLoop(context.Background(), reader, "test-model", testSession(t, harnessSession.OpenAICompatNative), vLLMChatURL, "")
	if !errors.Is(err, ErrMalformedJSON) {
		t.Fatalf("want abort wrapping ErrMalformedJSON, got: %v", err)
	}
	if got := atomic.LoadInt64(calls); got != maxBadBodies+1 {
		t.Fatalf("want %d provider calls (1 + %d retries), got %d", maxBadBodies+1, maxBadBodies, got)
	}
}

func envelopeChatURLs() map[string]*string {
	return map[string]*string{
		"Ollama":     &ollamaChatURL,
		"vLLM":       &vLLMChatURL,
		"Openrouter": &openRouterChatURL,
	}
}

func TestNonNativeMalformedHTTPBodyAbortsAfterBudget(t *testing.T) {
	for provider, urlVar := range envelopeChatURLs() {
		t.Run(provider, func(t *testing.T) {
			if provider == "Openrouter" {
				stubOpenRouterAuth(t, "secret")
			}

			reader := bufio.NewReader(strings.NewReader(""))
			calls := garbageServer(t, urlVar)
			_, err := nonNativeAgentCall(context.Background(), testSession(t, harnessSession.Envelope), reader, provider, "test-model")
			if !errors.Is(err, ErrMalformedJSON) {
				t.Fatalf("want abort wrapping ErrMalformedJSON, got: %v", err)
			}
			if got := atomic.LoadInt64(calls); got != maxBadBodies+1 {
				t.Fatalf("want %d provider calls (1 + %d retries), got %d", maxBadBodies+1, maxBadBodies, got)
			}
		})
	}
}

func TestNonNativeMalformedHTTPBodyRecovers(t *testing.T) {
	wrappers := map[string]func(string) string{
		"Ollama":     ollamaEnvelopeBody,
		"vLLM":       vllmEnvelopeBody,
		"Openrouter": vllmEnvelopeBody,
	}

	for provider, wrapBody := range wrappers {
		t.Run(provider, func(t *testing.T) {
			if provider == "Openrouter" {
				stubOpenRouterAuth(t, "secret")
			}

			calls := scriptedJSONServer(t, envelopeChatURLs()[provider], func(call int64) string {
				if call <= 2 {
					return "not json {{{"
				}
				return wrapBody(envelopeFinalAnswer)
			})

			sess := testSession(t, harnessSession.Envelope)
			reader := bufio.NewReader(strings.NewReader(""))
			out, err := nonNativeAgentCall(context.Background(), sess, reader, provider, "test-model")
			if err != nil {
				t.Fatalf("want recovery with answer, got error: %v", err)
			}
			if out != "done" {
				t.Fatalf("want %q, got %q", "done", out)
			}
			if got := atomic.LoadInt64(calls); got != 3 {
				t.Fatalf("want 3 provider calls (2 corrections + success), got %d", got)
			}

			msgs := *sess.Envelope()
			last := msgs[len(msgs)-1]
			if last.Role != "assistant" || !strings.Contains(last.Content, "final_answer") {
				t.Fatalf("final assistant envelope missing from history: %+v", last)
			}
		})
	}
}
