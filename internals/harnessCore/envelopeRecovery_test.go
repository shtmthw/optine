package harnessCore

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestNonNativeMalformedEnvelopeRecovers proves the full correction loop:
// garbage twice, then a valid final answer. The run must return the answer,
// having spent exactly 3 provider calls, with the excerpt-carrying correction
// doing the rescue.
func TestNonNativeMalformedEnvelopeRecovers(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		content := "```json this is not an envelope"
		if n > 2 {
			content = `{"type":"final_answer","content":"recovered!"}`
		}
		fmt.Fprintf(w, `{"message":{"role":"assistant","content":%q}}`, content)
	}))
	t.Cleanup(srv.Close)
	old := ollamaChatURL
	ollamaChatURL = srv.URL
	t.Cleanup(func() { ollamaChatURL = old })

	reader := bufio.NewReader(strings.NewReader(""))
	out, err := nonNativeAgentCall(context.Background(), reader, "Ollama", "test-model", "hi")
	if err != nil {
		t.Fatalf("want recovery with answer, got error: %v", err)
	}
	if out != "recovered!" {
		t.Fatalf("want %q, got %q", "recovered!", out)
	}
	if got := atomic.LoadInt64(&calls); got != 3 {
		t.Fatalf("want 3 provider calls (2 corrections + success), got %d", got)
	}
}

// TestNonNativeMalformedEnvelopeAbortsAfterBudget serves garbage forever: the
// run must abort past the shared budget with the provider named in the error.
func TestNonNativeMalformedEnvelopeAbortsAfterBudget(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"message":{"role":"assistant","content":"still garbage {{{"}}`)
	}))
	t.Cleanup(srv.Close)
	old := ollamaChatURL
	ollamaChatURL = srv.URL
	t.Cleanup(func() { ollamaChatURL = old })

	reader := bufio.NewReader(strings.NewReader(""))
	_, err := nonNativeAgentCall(context.Background(), reader, "Ollama", "test-model", "hi")
	if err == nil || !strings.Contains(err.Error(), "Ollama") {
		t.Fatalf("want abort naming the provider, got: %v", err)
	}
	if got := atomic.LoadInt64(&calls); got != maxBadBodies+1 {
		t.Fatalf("want %d provider calls, got %d", maxBadBodies+1, got)
	}
}

func TestCorrectionForBadEnvelope(t *testing.T) {
	raw := "```json\n" + strings.Repeat("x", maxEnvelopeExcerpt+100)
	out := correctionForBadEnvelope(raw)
	if !strings.Contains(out, "exactly one valid JSON object") {
		t.Fatalf("correction lacks the instruction: %q", out)
	}
	if !strings.Contains(out, "```json") {
		t.Fatalf("correction lacks the excerpt: %q", out)
	}
	if len([]rune(out)) > len("Your last reply was not a valid tool-call envelope. Reply with exactly one valid JSON object and nothing else, no markdown fences. What you sent was:\n")+maxEnvelopeExcerpt+50 {
		t.Fatalf("excerpt not capped: %d runes", len([]rune(out)))
	}
}
