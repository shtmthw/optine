package harnessCore

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/mattthew/optine/internal/dataTypes"
	"github.com/mattthew/optine/internal/harnessSession"
)

func TestWireEffort(t *testing.T) {
	all := harnessSession.NewEffortSupport([]harnessSession.Effort{
		harnessSession.EffortOff, harnessSession.EffortOn, harnessSession.EffortMinimal, harnessSession.EffortLow,
		harnessSession.EffortMedium, harnessSession.EffortHigh, harnessSession.EffortXHigh, harnessSession.EffortMax,
	}, harnessSession.EffortDefault)
	lowOnly := harnessSession.NewEffortSupport([]harnessSession.Effort{harnessSession.EffortLow}, harnessSession.EffortDefault)

	reasoning := func(effort string) effortWire {
		return effortWire{Reasoning: &dataTypes.ReasoningRequest{Effort: effort}}
	}

	for _, tc := range []struct {
		provider string
		support  harnessSession.EffortSupport
		level    harnessSession.Effort
		want     effortWire
	}{
		// Nothing is sent at default, without support, or for a level the
		// model does not take.
		{"Ollama", all, harnessSession.EffortDefault, effortWire{}},
		{"Ollama", harnessSession.EffortSupport{}, harnessSession.EffortHigh, effortWire{}},
		{"Openrouter", lowOnly, harnessSession.EffortHigh, effortWire{}},

		{"Ollama", all, harnessSession.EffortOff, effortWire{Think: false}},
		{"Ollama", all, harnessSession.EffortOn, effortWire{Think: true}},
		{"Ollama", all, harnessSession.EffortHigh, effortWire{Think: "high"}},

		{"Openrouter", all, harnessSession.EffortOff, reasoning("none")},
		{"Openrouter", all, harnessSession.EffortXHigh, reasoning("xhigh")},
		{"Openrouter", lowOnly, harnessSession.EffortLow, reasoning("low")},
		{"Openrouter", all, harnessSession.EffortOn, effortWire{}},

		{"vLLM", all, harnessSession.EffortOff, effortWire{ReasoningEffort: "none"}},
		{"vLLM", all, harnessSession.EffortMedium, effortWire{ReasoningEffort: "medium"}},
		{"vLLM", all, harnessSession.EffortMax, effortWire{}},

		{"Unknown", all, harnessSession.EffortHigh, effortWire{}},
	} {
		name := fmt.Sprintf("%s/%s/%d", tc.provider, tc.level, len(tc.support.Levels))
		if got := wireEffort(tc.provider, tc.support, tc.level); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: wireEffort = %+v, want %+v", name, got, tc.want)
		}
	}
}

// requestJSON is a builder's request as the provider receives it.
func requestJSON(t *testing.T, req any) map[string]any {
	t.Helper()

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBuildersCarryTheEffortField(t *testing.T) {
	msgs := []harnessSession.Message{{Role: "user", Content: "hi"}}
	off := effortWire{Think: false}
	or := effortWire{Reasoning: &dataTypes.ReasoningRequest{Effort: "high"}}
	vllm := effortWire{ReasoningEffort: "low"}

	for name, body := range map[string]map[string]any{
		"ollama native off":   requestJSON(t, produceOllamaReqBody(msgs, "m", "", off)),
		"ollama envelope off": requestJSON(t, produceNonNativeReqBody(msgs, "m", "", off)),
	} {
		if v, ok := body["think"]; !ok || v != false {
			t.Fatalf("%s: think = %v (present %v), want false sent", name, v, ok)
		}
	}

	for name, body := range map[string]map[string]any{
		"openai native":   requestJSON(t, produceOpenAIReqBody(msgs, "m", "", or)),
		"openai envelope": requestJSON(t, produceOpenAINonNativeReqBody(msgs, "m", "", or)),
	} {
		r, _ := body["reasoning"].(map[string]any)
		if r["effort"] != "high" {
			t.Fatalf("%s: reasoning = %v, want effort high", name, body["reasoning"])
		}
		if _, ok := body["reasoning_effort"]; ok {
			t.Fatalf("%s: OpenRouter request also carries reasoning_effort", name)
		}
	}

	for name, body := range map[string]map[string]any{
		"vllm native":   requestJSON(t, produceOpenAIReqBody(msgs, "m", "", vllm)),
		"vllm envelope": requestJSON(t, produceOpenAINonNativeReqBody(msgs, "m", "", vllm)),
	} {
		if body["reasoning_effort"] != "low" {
			t.Fatalf("%s: reasoning_effort = %v, want low", name, body["reasoning_effort"])
		}
	}

	for name, body := range map[string]map[string]any{
		"ollama native":   requestJSON(t, produceOllamaReqBody(msgs, "m", "", effortWire{})),
		"ollama envelope": requestJSON(t, produceNonNativeReqBody(msgs, "m", "", effortWire{})),
		"openai native":   requestJSON(t, produceOpenAIReqBody(msgs, "m", "", effortWire{})),
		"openai envelope": requestJSON(t, produceOpenAINonNativeReqBody(msgs, "m", "", effortWire{})),
	} {
		for _, field := range []string{"think", "reasoning", "reasoning_effort"} {
			if _, ok := body[field]; ok {
				t.Fatalf("%s: an unset level still sent %s", name, field)
			}
		}
	}
}

// A run sends the session's level on every request, and an envelope run on
// Ollama still parses the JSON answer with thinking on: Ollama returns the
// thinking text in its own field.
func TestRunSendsTheSessionEffort(t *testing.T) {
	var thinks []any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		thinks = append(thinks, req["think"])
		fmt.Fprint(w, `{"message":{"role":"assistant","thinking":"the user says hi","content":"{\"type\":\"final_answer\",\"content\":\"hello\"}"},"prompt_eval_count":3}`)
	}))
	t.Cleanup(srv.Close)

	old := ollamaChatURL
	ollamaChatURL = srv.URL
	t.Cleanup(func() { ollamaChatURL = old })

	sess := testSession(t, harnessSession.Envelope)
	sess.Effort = harnessSession.EffortHigh
	sess.EffortSupport = harnessSession.NewEffortSupport([]harnessSession.Effort{harnessSession.EffortLow, harnessSession.EffortHigh}, harnessSession.EffortDefault)
	sess.AppendUser("hi")

	out, err := nonNativeAgentCall(context.Background(), sess, bufio.NewReader(strings.NewReader("")), "Ollama", "test-model")
	if err != nil || out != "hello" {
		t.Fatalf("answer = %q, %v; want hello", out, err)
	}
	if len(thinks) != 1 || thinks[0] != "high" {
		t.Fatalf("think sent = %v, want [high]", thinks)
	}
}
