package harnessCore

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mattthew/optine/internal/dataTypes"
	"github.com/mattthew/optine/internal/harnessSession"
)

const (
	compactedMarker = "SUMMARY-OF-TURN-ONE"
	firstTurnAnswer = "first turn answer"

	// overLimit is a prompt size past the auto-compaction threshold of a
	// session with the default context limit.
	overLimit = harnessSession.DefaultContextLimit
)

// withPromptTokens rewrites the prompt size a scripted provider body reports.
func withPromptTokens(body string, n int) string {
	body = strings.Replace(body, `"prompt_eval_count":1`, fmt.Sprintf(`"prompt_eval_count":%d`, n), 1)

	return strings.Replace(body, `"prompt_tokens":1`, fmt.Sprintf(`"prompt_tokens":%d`, n), 1)
}

// requestLog keeps every request body a scripted provider received, so a test
// can see which history each request was built from.
type requestLog struct {
	mu     sync.Mutex
	bodies []string
}

func (l *requestLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]string(nil), l.bodies...)
}

// recordingServer answers request n (1-based) with body(n) and logs the
// request. It points one endpoint var at the server and restores it after.
func recordingServer(t *testing.T, urlVar *string, body func(call int) string) *requestLog {
	t.Helper()

	log := &requestLog{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)

		log.mu.Lock()
		log.bodies = append(log.bodies, string(raw))
		call := len(log.bodies)
		log.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body(call))
	}))
	t.Cleanup(srv.Close)

	old := *urlVar
	*urlVar = srv.URL
	t.Cleanup(func() { *urlVar = old })

	return log
}

// midRunLoops is every loop that can compact in the middle of a run.
var midRunLoops = []struct {
	name     string
	kind     harnessSession.Kind
	urlVar   *string
	toolCall func() string
	final    func() string
	run      func(reSess, sess *harnessSession.Session, compact CompactFunc) (string, error)
}{
	{
		name:     "ollama native",
		kind:     harnessSession.OllamaNative,
		urlVar:   &ollamaChatURL,
		toolCall: ollamaNativeToolCall,
		final:    ollamaNativeFinalAnswer,
		run: func(reSess, sess *harnessSession.Session, compact CompactFunc) (string, error) {
			return ollamaToolLoop(context.Background(), bufio.NewReader(strings.NewReader("")), "test-model", reSess, sess, compact)
		},
	},
	{
		name:     "vllm native",
		kind:     harnessSession.OpenAICompatNative,
		urlVar:   &vLLMChatURL,
		toolCall: vllmNativeToolCall,
		final:    vllmNativeFinalAnswer,
		run: func(reSess, sess *harnessSession.Session, compact CompactFunc) (string, error) {
			return openAiToolLoop(context.Background(), bufio.NewReader(strings.NewReader("")), "test-model", reSess, sess, vLLMChatURL, "", compact)
		},
	},
	{
		name:     "ollama envelope",
		kind:     harnessSession.Envelope,
		urlVar:   &ollamaChatURL,
		toolCall: func() string { return ollamaEnvelopeBody(envelopeToolCall) },
		final:    func() string { return ollamaEnvelopeBody(envelopeFinalAnswer) },
		run: func(reSess, sess *harnessSession.Session, compact CompactFunc) (string, error) {
			return nonNativeAgentCall(context.Background(), reSess, sess, bufio.NewReader(strings.NewReader("")), "Ollama", "test-model", compact)
		},
	},
}

// addFirstTurn gives sess one finished turn for a compaction to summarise.
func addFirstTurn(sess *harnessSession.Session) {
	sess.AppendUser("first question")
	sess.Messages = append(sess.Messages, harnessSession.Message{Role: "assistant", Content: firstTurnAnswer})
}

// twoTurnSession has one finished turn and a second user message waiting, the
// way AgentLoop leaves a session before it calls a loop.
func twoTurnSession(t *testing.T, kind harnessSession.Kind) *harnessSession.Session {
	t.Helper()

	sess := testSession(t, kind)
	addFirstTurn(sess)
	sess.AppendUser("second question")

	return sess
}

// compactTurnOne stands in for the cli package's compaction: it leaves sess
// the way the real one does (flag, summary, cleared prompt size) with the
// first turn compacted, and returns the reconstructed session.
func compactTurnOne(t *testing.T, sess *harnessSession.Session) *harnessSession.Session {
	t.Helper()

	sess.Compacted = true
	sess.Compaction = harnessSession.Compaction{Summary: compactedMarker, ThroughTurn: 1}
	sess.LastPromptTokens = 0

	reSess, err := harnessSession.ReconstructSession(sess)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}

	return reSess
}

// A run whose requests outgrow the context window is compacted between two
// requests and then carries on by itself, on the reconstructed session. Both
// tool-calling responses report a nearly full window, but the cutoff cannot
// move during a run, so only the first one triggers a compaction.
func TestMidRunCompactionContinuesOnReconstructedSession(t *testing.T) {
	for _, loop := range midRunLoops {
		t.Run(loop.name, func(t *testing.T) {
			log := recordingServer(t, loop.urlVar, func(call int) string {
				if call <= 2 {
					return withPromptTokens(loop.toolCall(), overLimit)
				}

				return loop.final()
			})

			sess := twoTurnSession(t, loop.kind)

			var reSess *harnessSession.Session
			compactions := 0

			out, err := loop.run(nil, sess, func() *harnessSession.Session {
				compactions++
				reSess = compactTurnOne(t, sess)

				return reSess
			})
			if err != nil || out != "done" {
				t.Fatalf("run did not finish after compacting: out=%q err=%v", out, err)
			}
			if compactions != 1 {
				t.Fatalf("compactions = %d, want 1 per run", compactions)
			}

			bodies := log.all()
			if len(bodies) != 3 {
				t.Fatalf("provider requests = %d, want 3", len(bodies))
			}
			if !strings.Contains(bodies[0], firstTurnAnswer) || strings.Contains(bodies[0], compactedMarker) {
				t.Fatalf("request 1 should carry the full history:\n%s", bodies[0])
			}
			for i, body := range bodies[1:] {
				if !strings.Contains(body, compactedMarker) || strings.Contains(body, firstTurnAnswer) {
					t.Fatalf("request %d should carry the summary in place of turn one:\n%s", i+2, body)
				}
			}

			// What the run added after the compaction is in both histories;
			// the reconstructed one is shorter by turn one, less its summary.
			if got, want := len(reSess.Messages), len(sess.Messages)-1; got != want {
				t.Fatalf("reconstructed session has %d messages, want %d", got, want)
			}
			if a, b := reSess.Messages[len(reSess.Messages)-1], sess.Messages[len(sess.Messages)-1]; a.Content != b.Content {
				t.Fatalf("final answer missing from the reconstructed session: %q vs %q", a.Content, b.Content)
			}
		})
	}
}

// However full the window already is, a run's first request still goes out:
// the prompt size only changes when a request is made, so compacting before
// it could leave a session that never answers again.
func TestNoMidRunCompactionBeforeFirstRequest(t *testing.T) {
	for _, loop := range midRunLoops {
		t.Run(loop.name, func(t *testing.T) {
			log := recordingServer(t, loop.urlVar, func(int) string {
				return withPromptTokens(loop.final(), overLimit)
			})

			sess := twoTurnSession(t, loop.kind)
			sess.LastPromptTokens = overLimit

			compactions := 0

			out, err := loop.run(nil, sess, func() *harnessSession.Session {
				compactions++

				return nil
			})
			if err != nil || out != "done" {
				t.Fatalf("out=%q err=%v", out, err)
			}
			if compactions != 0 {
				t.Fatalf("compactions = %d, want 0: the run made no second request", compactions)
			}
			if n := len(log.all()); n != 1 {
				t.Fatalf("provider requests = %d, want 1", n)
			}
		})
	}
}

// A compaction that compacts nothing hands back the session it was given. The
// run goes on with the full history and does not ask again.
func TestFailedMidRunCompactionKeepsRunGoing(t *testing.T) {
	for _, loop := range midRunLoops {
		t.Run(loop.name, func(t *testing.T) {
			log := recordingServer(t, loop.urlVar, func(call int) string {
				if call <= 2 {
					return withPromptTokens(loop.toolCall(), overLimit)
				}

				return loop.final()
			})

			sess := twoTurnSession(t, loop.kind)
			compactions := 0

			out, err := loop.run(nil, sess, func() *harnessSession.Session {
				compactions++

				return nil
			})
			if err != nil || out != "done" {
				t.Fatalf("out=%q err=%v", out, err)
			}
			if compactions != 1 {
				t.Fatalf("compactions = %d, want 1 attempt per run", compactions)
			}

			bodies := log.all()
			if len(bodies) != 3 {
				t.Fatalf("provider requests = %d, want 3", len(bodies))
			}
			for i, body := range bodies {
				if !strings.Contains(body, firstTurnAnswer) {
					t.Fatalf("request %d should still carry the full history:\n%s", i+1, body)
				}
			}
		})
	}
}

// A loop given no callback never compacts, which is what keeps compaction's
// own summary run from compacting itself.
func TestNoCallbackNoMidRunCompaction(t *testing.T) {
	for _, loop := range midRunLoops {
		t.Run(loop.name, func(t *testing.T) {
			log := recordingServer(t, loop.urlVar, func(call int) string {
				if call == 1 {
					return withPromptTokens(loop.toolCall(), overLimit)
				}

				return loop.final()
			})

			out, err := loop.run(nil, twoTurnSession(t, loop.kind), nil)
			if err != nil || out != "done" {
				t.Fatalf("out=%q err=%v", out, err)
			}
			if n := len(log.all()); n != 2 {
				t.Fatalf("provider requests = %d, want 2", n)
			}
		})
	}
}

// A first compaction in the middle of a run gives AgentLoop a reconstructed
// session it did not start with. Interrupting right after it must put the
// notice on that session, and the run must still be saved.
func TestAgentLoopFollowsMidRunCompaction(t *testing.T) {
	sess := interruptEnv(t)
	addFirstTurn(sess)

	recordingServer(t, &ollamaChatURL, func(int) string {
		return withPromptTokens(ollamaNativeToolCall(), overLimit)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var reSess *harnessSession.Session

	_, err := AgentLoop(ctx, nil, sess,
		dataTypes.AgentConfig{Provider: "Ollama", Model: "test-model", NativeToolCalling: true},
		"second question", bufio.NewReader(strings.NewReader("")),
		func() *harnessSession.Session {
			reSess = compactTurnOne(t, sess)
			// The user stops the run while it is compacting.
			cancel()

			return reSess
		})
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("err = %v, want ErrInterrupted", err)
	}
	if reSess == nil {
		t.Fatal("the run never compacted")
	}
	if last := reSess.Messages[len(reSess.Messages)-1]; last.Content != harnessSession.InterruptedNotice {
		t.Fatalf("interrupt notice missing from the reconstructed session: %+v", last)
	}

	loaded, err := harnessSession.Load(sess.UID)
	if err != nil {
		t.Fatalf("run was not saved after a mid-run compaction: %v", err)
	}
	if last := loaded.Messages[len(loaded.Messages)-1]; !loaded.Compacted || last.Content != harnessSession.InterruptedNotice {
		t.Fatalf("saved session: compacted=%v last=%+v", loaded.Compacted, last)
	}
}
