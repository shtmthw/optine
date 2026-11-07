package harnessCore

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattthew/optine/internal/dataTypes"
	"github.com/mattthew/optine/internal/harnessPermissions"
	"github.com/mattthew/optine/internal/harnessSession"
)

// interruptEnv redirects session files and snapshots to a temp dir and
// returns a fresh non-git session rooted in its own temp directory.
func interruptEnv(t *testing.T) *harnessSession.Session {
	t.Helper()

	oldAuthPath := harnessPermissions.AuthPath
	harnessPermissions.AuthPath = filepath.Join(t.TempDir(), "auth.json")
	t.Cleanup(func() { harnessPermissions.AuthPath = oldAuthPath })

	return harnessSession.New(harnessSession.Config{
		Provider:  "Ollama",
		Model:     "test-model",
		Directory: t.TempDir(),
		Kind:      harnessSession.OllamaNative,
		MaxTurns:  MaxTurns,
		ToolCall:  true,
	})
}

func runAgentLoop(ctx context.Context, sess *harnessSession.Session, msg string) (string, error) {
	return AgentLoop(ctx, sess,
		dataTypes.AgentConfig{Provider: "Ollama", Model: "test-model", NativeToolCalling: true},
		msg, bufio.NewReader(strings.NewReader("")))
}

// Interrupting while the provider is still thinking aborts the request,
// returns ErrInterrupted, and leaves the user turn plus the interrupt marker
// committed to disk.
func TestAgentLoopInterruptDuringInferenceCommitsHistory(t *testing.T) {
	sess := interruptEnv(t)

	started := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		once.Do(func() { close(started) })
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	old := ollamaChatURL
	ollamaChatURL = srv.URL
	t.Cleanup(func() { ollamaChatURL = old })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type outcome struct {
		answer string
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		answer, err := runAgentLoop(ctx, sess, "long task")
		done <- outcome{answer, err}
	}()

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("provider never saw the request")
	}
	cancel()

	var got outcome
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("AgentLoop did not return after the interrupt")
	}

	if !errors.Is(got.err, ErrInterrupted) || !errors.Is(got.err, context.Canceled) {
		t.Fatalf("err = %v, want ErrInterrupted wrapping context.Canceled", got.err)
	}
	if got.answer != "" {
		t.Fatalf("interrupted run returned answer %q", got.answer)
	}

	loaded, err := harnessSession.Load(sess.UID)
	if err != nil {
		t.Fatalf("interrupted session was not saved: %v", err)
	}
	assertRoles(t, loaded.Messages, "user", "user")
	if loaded.Messages[0].Content != "long task" {
		t.Fatalf("user turn lost: %+v", loaded.Messages[0])
	}
	if loaded.Messages[1].Content != harnessSession.InterruptedNotice {
		t.Fatalf("interrupt marker missing: %+v", loaded.Messages[1])
	}
	if loaded.TotalUserMessages != 1 {
		t.Fatalf("marker counted as a user message: %d", loaded.TotalUserMessages)
	}
}

// Interrupting while a turn's tools run skips the remaining calls, still
// pairs every call with a result (Load rejects unpaired calls), makes no
// further inference request, and commits the whole partial turn.
func TestAgentLoopInterruptDuringToolsCommitsPairedTurn(t *testing.T) {
	sess := interruptEnv(t)

	var requests int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		atomic.AddInt64(&requests, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"message":{"role":"assistant","content":"working on it","tool_calls":[`+
			`{"id":"c1","function":{"name":"bash","arguments":{"command":"echo one"}}},`+
			`{"id":"c2","function":{"name":"bash","arguments":{"command":"echo two"}}}]}}`)
	}))
	t.Cleanup(srv.Close)
	old := ollamaChatURL
	ollamaChatURL = srv.URL
	t.Cleanup(func() { ollamaChatURL = old })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The user hits Esc twice the moment the first tool call shows up.
	harnessSession.SetDisplayEventListener(func(ev harnessSession.DisplayEvent) {
		if ev.Role == harnessSession.TranscriptToolCall {
			cancel()
		}
	})
	t.Cleanup(func() { harnessSession.SetDisplayEventListener(nil) })

	_, err := runAgentLoop(ctx, sess, "run two commands")
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("err = %v, want ErrInterrupted", err)
	}
	if n := atomic.LoadInt64(&requests); n != 1 {
		t.Fatalf("provider requests = %d, want 1 (no inference after the interrupt)", n)
	}

	loaded, err := harnessSession.Load(sess.UID)
	if err != nil {
		t.Fatalf("interrupted session was not saved or failed validation: %v", err)
	}
	assertRoles(t, loaded.Messages, "user", "assistant", "tool", "tool", "user")

	for i, id := range []string{"c1", "c2"} {
		res := loaded.Messages[2+i]
		if res.ToolCallID != id || res.Content != interruptedToolResult {
			t.Fatalf("tool result %d = %+v, want interrupted result for %s", i, res, id)
		}
	}
	if loaded.Messages[4].Content != harnessSession.InterruptedNotice {
		t.Fatalf("interrupt marker missing: %+v", loaded.Messages[4])
	}

	// The next message continues the same history after the marker.
	fakeChatServer(t, `{"message":{"role":"assistant","content":"resumed"}}`)
	answer, err := runAgentLoop(context.Background(), loaded, "carry on")
	if err != nil || answer != "resumed" {
		t.Fatalf("follow-up run: answer=%q err=%v", answer, err)
	}
	assertRoles(t, loaded.Messages, "user", "assistant", "tool", "tool", "user", "user", "assistant")
}

// A run that already produced its answer is complete even if the interrupt
// lands right after; the answer is kept and no marker is added.
func TestAgentLoopKeepsAnswerWhenInterruptArrivesLate(t *testing.T) {
	sess := interruptEnv(t)
	fakeChatServer(t, `{"message":{"role":"assistant","content":"done"}}`)

	ctx, cancel := context.WithCancel(context.Background())
	answer, err := runAgentLoop(ctx, sess, "quick")
	cancel()

	if err != nil || answer != "done" {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
	for _, msg := range sess.Messages {
		if msg.Content == harnessSession.InterruptedNotice {
			t.Fatal("completed run was marked interrupted")
		}
	}
}

// A tool call reached after the interrupt is never dispatched.
func TestRunToolSkipsAfterInterrupt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got := runTool(ctx, bufio.NewReader(strings.NewReader("y\n")), "write_file",
		map[string]any{"path": filepath.Join(t.TempDir(), "x.txt"), "content": "nope"})
	if got != interruptedToolResult {
		t.Fatalf("result = %q, want %q", got, interruptedToolResult)
	}
}

func assertRoles(t *testing.T, msgs []harnessSession.Message, want ...string) {
	t.Helper()

	got := make([]string, len(msgs))
	for i, m := range msgs {
		got[i] = m.Role
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("roles = %v, want %v", got, want)
	}
}

// The envelope (non-native) loop: interrupting while its tool runs keeps the
// tool_call envelope and its TOOL_RESULT turn, makes no further inference
// request, and commits both plus the marker. On resume the transcript shows
// the call, the interrupted result, and the marker as an event.
func TestEnvelopeLoopInterruptDuringToolCommitsHistory(t *testing.T) {
	sess := interruptEnv(t)

	var requests int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		atomic.AddInt64(&requests, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"message":{"role":"assistant","content":"{\"type\":\"tool_call\",\"tool\":\"bash\",\"arguments\":{\"command\":\"echo hi\"}}"}}`)
	}))
	t.Cleanup(srv.Close)
	old := ollamaChatURL
	ollamaChatURL = srv.URL
	t.Cleanup(func() { ollamaChatURL = old })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	harnessSession.SetDisplayEventListener(func(ev harnessSession.DisplayEvent) {
		if ev.Role == harnessSession.TranscriptToolCall {
			cancel()
		}
	})
	t.Cleanup(func() { harnessSession.SetDisplayEventListener(nil) })

	_, err := AgentLoop(ctx, sess,
		dataTypes.AgentConfig{Provider: "Ollama", Model: "test-model", NativeToolCalling: false},
		"run a command", bufio.NewReader(strings.NewReader("")))
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("err = %v, want ErrInterrupted", err)
	}
	if n := atomic.LoadInt64(&requests); n != 1 {
		t.Fatalf("provider requests = %d, want 1 (no inference after the interrupt)", n)
	}

	loaded, err := harnessSession.Load(sess.UID)
	if err != nil {
		t.Fatalf("interrupted envelope session was not saved: %v", err)
	}
	if loaded.Kind != harnessSession.Envelope {
		t.Fatalf("kind = %v, want Envelope", loaded.Kind)
	}
	assertRoles(t, loaded.Messages, "user", "assistant", "user", "user")
	if !strings.Contains(loaded.Messages[1].Content, `"tool_call"`) {
		t.Fatalf("tool_call envelope lost: %+v", loaded.Messages[1])
	}
	if res := loaded.Messages[2].Content; !strings.HasPrefix(res, "TOOL_RESULT") || !strings.Contains(res, interruptedToolResult) {
		t.Fatalf("TOOL_RESULT turn = %q, want the interrupted result", res)
	}
	if loaded.Messages[3].Content != harnessSession.InterruptedNotice {
		t.Fatalf("interrupt marker missing: %+v", loaded.Messages[3])
	}

	var roles []string
	for _, e := range loaded.Transcript() {
		roles = append(roles, string(e.Role))
	}
	if got := strings.Join(roles, ","); got != "user,tool_call,tool,event" {
		t.Fatalf("resumed transcript roles = %s, want user,tool_call,tool,event", got)
	}

	// The next message continues the envelope history after the marker.
	fakeChatServer(t, `{"message":{"role":"assistant","content":"{\"type\":\"final_answer\",\"content\":\"resumed\"}"}}`)
	answer, err := AgentLoop(context.Background(), loaded,
		dataTypes.AgentConfig{Provider: "Ollama", Model: "test-model", NativeToolCalling: false},
		"carry on", bufio.NewReader(strings.NewReader("")))
	if err != nil || answer != "resumed" {
		t.Fatalf("follow-up run: answer=%q err=%v", answer, err)
	}
}

// Interrupting the envelope loop while the model is still thinking.
func TestEnvelopeLoopInterruptDuringInferenceCommitsHistory(t *testing.T) {
	sess := interruptEnv(t)

	started := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		once.Do(func() { close(started) })
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	old := ollamaChatURL
	ollamaChatURL = srv.URL
	t.Cleanup(func() { ollamaChatURL = old })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := AgentLoop(ctx, sess,
			dataTypes.AgentConfig{Provider: "Ollama", Model: "test-model", NativeToolCalling: false},
			"long task", bufio.NewReader(strings.NewReader("")))
		done <- err
	}()

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("provider never saw the request")
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, ErrInterrupted) {
			t.Fatalf("err = %v, want ErrInterrupted", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("envelope loop did not return after the interrupt")
	}

	loaded, err := harnessSession.Load(sess.UID)
	if err != nil {
		t.Fatalf("interrupted envelope session was not saved: %v", err)
	}
	assertRoles(t, loaded.Messages, "user", "user")
	if loaded.Messages[1].Content != harnessSession.InterruptedNotice {
		t.Fatalf("interrupt marker missing: %+v", loaded.Messages[1])
	}
}
