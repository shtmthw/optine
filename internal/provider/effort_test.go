package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/mattthew/optine/internal/dataTypes"
	"github.com/mattthew/optine/internal/harnessSession"
)

type effort = harnessSession.Effort

const (
	off     = harnessSession.EffortOff
	on      = harnessSession.EffortOn
	minimal = harnessSession.EffortMinimal
	low     = harnessSession.EffortLow
	medium  = harnessSession.EffortMedium
	high    = harnessSession.EffortHigh
	xhigh   = harnessSession.EffortXHigh
	maxLvl  = harnessSession.EffortMax
	unset   = harnessSession.EffortDefault
)

func openRouterModel(t *testing.T, raw string) dataTypes.OpenRouterModel {
	t.Helper()

	var m dataTypes.OpenRouterModel
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestOpenRouterEffortSupport(t *testing.T) {
	for _, tc := range []struct {
		name   string
		raw    string
		levels []effort
		def    effort
	}{
		{"no reasoning object", `{"id":"m"}`, nil, unset},
		{"reasoning parameter only", `{"id":"m","supported_parameters":["tools","reasoning"]}`, []effort{low, medium, high}, unset},
		{"supported_efforts missing", `{"id":"m","reasoning":{"default_effort":"high"}}`, nil, unset},
		{"supported_efforts null", `{"id":"m","reasoning":{"supported_efforts":null}}`, []effort{off, minimal, low, medium, high, xhigh, maxLvl}, unset},
		{"a list", `{"id":"m","reasoning":{"supported_efforts":["high","none","low","bogus"],"default_effort":"low"}}`, []effort{off, low, high}, low},
		{"mandatory", `{"id":"m","reasoning":{"supported_efforts":null,"mandatory":true,"default_effort":"medium"}}`, []effort{minimal, low, medium, high, xhigh, maxLvl}, medium},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := OpenRouterEffortSupport(openRouterModel(t, tc.raw))
			if !slices.Equal(got.Levels, tc.levels) || got.Default != tc.def {
				t.Fatalf("support = %+v, want levels %v default %q", got, tc.levels, tc.def)
			}
		})
	}
}

func TestOllamaEffortSupport(t *testing.T) {
	for _, tc := range []struct {
		name   string
		raw    string
		levels []effort
		def    effort
	}{
		{"string levels", `{"thinking":{"values":["low","medium","high"],"default":"medium"}}`, []effort{low, medium, high}, medium},
		{"booleans", `{"thinking":{"values":[true,false],"default":true}}`, []effort{off, on}, on},
		{"cannot think", `{"thinking":{"values":[false]}}`, nil, unset},
		{"no thinking object", `{"capabilities":["completion","tools"]}`, nil, unset},
		{"older Ollama with the capability", `{"capabilities":["completion","thinking"]}`, []effort{off, on}, unset},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var meta OllamaShowResponse
			if err := json.Unmarshal([]byte(tc.raw), &meta); err != nil {
				t.Fatal(err)
			}
			got := OllamaEffortSupport(meta)
			if !slices.Equal(got.Levels, tc.levels) || got.Default != tc.def {
				t.Fatalf("support = %+v, want levels %v default %q", got, tc.levels, tc.def)
			}
		})
	}
}

func TestVLLMProbeEffort(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"reasoning returned", 200, `{"choices":[{"message":{"content":"OK","reasoning":"the user wants OK"}}]}`, true},
		{"reasoning_content returned", 200, `{"choices":[{"message":{"content":"OK","reasoning_content":"think"}}]}`, true},
		{"no reasoning parser", 200, `{"choices":[{"message":{"content":"<think>hm</think>OK"}}]}`, false},
		{"field rejected", 400, `{"error":{"message":"unknown field"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubProvider(t, &vllmClient, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/chat/completions" {
					t.Errorf("path = %q", r.URL.Path)
				}
				var req map[string]any
				_ = json.NewDecoder(r.Body).Decode(&req)
				if req["reasoning_effort"] != "low" {
					t.Errorf("probe sent reasoning_effort %v, want low", req["reasoning_effort"])
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})

			got, err := VLLMProbeEffort(context.Background(), "qwen3")
			if err != nil || got != tc.want {
				t.Fatalf("VLLMProbeEffort = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

// EffortSupportFor gives vLLM its fixed levels after a good probe, and a hint
// to restart the server after a probe that saw no reasoning.
func TestEffortSupportForVLLM(t *testing.T) {
	stubProvider(t, &vllmClient, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}]}`))
	})

	got, err := EffortSupportFor(context.Background(), "vLLM", "qwen3")
	if err != nil || got.Supported() || got.Note == "" {
		t.Fatalf("support = %+v, %v; want none with a note", got, err)
	}

	stubProvider(t, &vllmClient, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"reasoning":"r"}}]}`))
	})

	got, err = EffortSupportFor(context.Background(), "vLLM", "qwen3")
	if err != nil || !slices.Equal(got.Levels, []effort{off, low, medium, high}) {
		t.Fatalf("support = %+v, %v; want off, low, medium, high", got, err)
	}
}

func TestEffortSupportForOpenRouterReadsTheCatalog(t *testing.T) {
	old := AllOpenrouterModels
	t.Cleanup(func() { AllOpenrouterModels = old })
	AllOpenrouterModels = []dataTypes.OpenRouterModel{openRouterModel(t, `{"id":"a/b","reasoning":{"supported_efforts":["low","high"]}}`)}

	got, err := EffortSupportFor(context.Background(), "Openrouter", "a/b")
	if err != nil || !slices.Equal(got.Levels, []effort{low, high}) {
		t.Fatalf("support = %+v, %v", got, err)
	}

	if _, err := EffortSupportFor(context.Background(), "Openrouter", "missing"); err == nil {
		t.Fatal("a model missing from the catalog should be an error")
	}
}
