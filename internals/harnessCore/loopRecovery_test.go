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
)

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
	_, err := ollamaToolLoop(context.Background(), reader, "test-model", "hi")
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
	_, err := vllmToolLoop(context.Background(), reader, "test-model", "hi")
	if !errors.Is(err, ErrMalformedJSON) {
		t.Fatalf("want abort wrapping ErrMalformedJSON, got: %v", err)
	}
	if got := atomic.LoadInt64(calls); got != maxBadBodies+1 {
		t.Fatalf("want %d provider calls (1 + %d retries), got %d", maxBadBodies+1, maxBadBodies, got)
	}
}
