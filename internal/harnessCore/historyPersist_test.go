package harnessCore

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattthew/optine/internal/harnessSession"
)

// fakeChatServer points both provider endpoints at one canned-response server.
func fakeChatServer(t *testing.T, body string) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)

	oldOllama, oldVLLM := ollamaChatURL, vLLMChatURL
	ollamaChatURL, vLLMChatURL = srv.URL, srv.URL
	t.Cleanup(func() {
		ollamaChatURL, vLLMChatURL = oldOllama, oldVLLM
	})
}

// TestOllamaLoopPersistsFinalAnswer: the final assistant turn must land in the
// shared history, not just be returned to the caller. Regression for session
// files that only ever contained user messages.
func TestOllamaLoopPersistsFinalAnswer(t *testing.T) {
	fakeChatServer(t, `{"message":{"role":"assistant","content":"done"},"prompt_eval_count":12}`)

	reader := bufio.NewReader(strings.NewReader(""))
	sess := testSession(t, harnessSession.OllamaNative)
	sess.BeginRun()
	sess.AppendUser("hi")

	out, err := ollamaToolLoop(context.Background(), reader, "test-model", sess)
	if err != nil {
		t.Fatalf("loop: %v", err)
	}

	if out != "done" {
		t.Fatalf("want answer %q, got %q", "done", out)
	}

	msgs := *sess.Ollama()
	last := msgs[len(msgs)-1]
	if last.Role != "assistant" || last.Content != "done" {
		t.Fatalf("final assistant turn missing from history: %+v", last)
	}

	if sess.LastPromptTokens != 12 {
		t.Fatalf("prompt tokens not recorded: %d", sess.LastPromptTokens)
	}
}

func TestVLLMLoopPersistsFinalAnswer(t *testing.T) {
	fakeChatServer(t, `{"choices":[{"message":{"role":"assistant","content":"done"}}],"usage":{"prompt_tokens":7}}`)

	reader := bufio.NewReader(strings.NewReader(""))
	sess := testSession(t, harnessSession.VLLMNative)
	sess.BeginRun()
	sess.AppendUser("hi")

	out, err := vllmToolLoop(context.Background(), reader, "test-model", sess)
	if err != nil {
		t.Fatalf("loop: %v", err)
	}

	if out != "done" {
		t.Fatalf("want answer %q, got %q", "done", out)
	}

	msgs := *sess.VLLM()
	last := msgs[len(msgs)-1]
	if last.Role != "assistant" || last.Content != "done" {
		t.Fatalf("final assistant turn missing from history: %+v", last)
	}

	if sess.LastPromptTokens != 7 {
		t.Fatalf("prompt tokens not recorded: %d", sess.LastPromptTokens)
	}
}

func TestEnvelopeLoopPersistsFinalAnswer(t *testing.T) {
	fakeChatServer(t, `{"message":{"role":"assistant","content":"{\"type\":\"final_answer\",\"content\":\"done\"}"}}`)

	reader := bufio.NewReader(strings.NewReader(""))
	sess := testSession(t, harnessSession.Envelope)
	sess.BeginRun()
	sess.AppendUser("hi")

	out, err := nonNativeAgentCall(context.Background(), sess, reader, "Ollama", "test-model")
	if err != nil {
		t.Fatalf("loop: %v", err)
	}

	if out != "done" {
		t.Fatalf("want answer %q, got %q", "done", out)
	}

	msgs := *sess.Envelope()
	last := msgs[len(msgs)-1]
	if last.Role != "assistant" || !strings.Contains(last.Content, "final_answer") {
		t.Fatalf("final assistant envelope missing from history: %+v", last)
	}
}
