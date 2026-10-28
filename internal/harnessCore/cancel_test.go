package harnessCore

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mattthew/optine/internal/harnessSession"
)

// TestOllamaToolLoopCancellation proves a canceled context aborts an
// in-flight inference call instead of running until the client timeout.
func TestOllamaToolLoopCancellation(t *testing.T) {
	var (
		serverCtx context.Context
		serverMu  sync.Mutex
		started   = make(chan struct{})
		done      = make(chan struct{})
	)

	// release guarantees the handler can exit even if the cancellation
	// assertion fails; otherwise srv.Close would wait forever.
	release := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The server only starts its disconnect-detecting background read
		// after the request body is fully consumed. Drain it first, or the
		// handler will never observe the client's cancellation.
		_, _ = io.Copy(io.Discard, r.Body)

		serverMu.Lock()
		serverCtx = r.Context()
		serverMu.Unlock()
		close(started)

		select {
		case <-r.Context().Done():
			close(done)
		case <-release:
		}
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})

	old := ollamaChatURL
	ollamaChatURL = srv.URL
	t.Cleanup(func() { ollamaChatURL = old })

	reader := bufio.NewReader(nil)
	ctx, cancel := context.WithCancel(context.Background())

	resultErr := make(chan error, 1)
	go func() {
		_, err := ollamaToolLoop(ctx, reader, "test-model", testSession(t, harnessSession.OllamaNative))
		resultErr <- err
	}()

	// Wait until the fake server has seen the request.
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("fake server never saw the request")
	}

	cancel()

	select {
	case err := <-resultErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("loop err = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("loop did not return after cancellation")
	}

	select {
	case <-done:
		// server observed cancellation
	case <-time.After(10 * time.Second):
		t.Fatal("server never observed request-context cancellation")
	}

	serverMu.Lock()
	defer serverMu.Unlock()
	if serverCtx.Err() != context.Canceled {
		t.Fatalf("server context err = %v, want Canceled", serverCtx.Err())
	}
}
