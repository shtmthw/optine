package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// redirectClient returns an *http.Client that sends every request to base,
// keeping the path, so the hardcoded localhost endpoints can be pointed at a
// test server without changing the provider source.
func redirectClient(base string) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			clone := req.Clone(req.Context())
			clone.URL.Scheme = "http"
			clone.URL.Host = base[len("http://"):]
			return http.DefaultTransport.RoundTrip(clone)
		}),
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// stubProvider points one package-level client at a test server for the
// duration of a test and restores it afterwards.
func stubProvider(t *testing.T, client **http.Client, handler http.HandlerFunc) {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	old := *client
	*client = redirectClient(srv.URL)
	t.Cleanup(func() { *client = old })
}

// ---------------------------------------------------------------- ollama

func TestOllamaGetModelData(t *testing.T) {
	stubProvider(t, &ollamaClient, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		fmt.Fprint(w, `{"models":[{"name":"llama3:8b"},{"name":"qwen2.5:7b"}]}`)
	})

	models, err := OllamaGetModelData()
	if err != nil {
		t.Fatalf("OllamaGetModelData: %v", err)
	}
	if len(models) != 2 || models[0] != "llama3:8b" || models[1] != "qwen2.5:7b" {
		t.Fatalf("unexpected models: %v", models)
	}
}

func TestOllamaGetModelDataNoModels(t *testing.T) {
	stubProvider(t, &ollamaClient, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"models":[]}`)
	})

	if _, err := OllamaGetModelData(); err == nil {
		t.Fatal("empty model list should error")
	}
}

func TestOllamaGetModelDataNeedsKey(t *testing.T) {
	stubProvider(t, &ollamaClient, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	if _, err := OllamaGetModelData(); !errors.Is(err, ErrNeedsKey) {
		t.Fatalf("want ErrNeedsKey, got %v", err)
	}
}

func TestOllamaGetModelDataUnexpectedStatus(t *testing.T) {
	stubProvider(t, &ollamaClient, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	if _, err := OllamaGetModelData(); err == nil {
		t.Fatal("non-OK status should error")
	}
}

func TestOllamaGetModelMetaData(t *testing.T) {
	stubProvider(t, &ollamaClient, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/show" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		fmt.Fprint(w, `{"capabilities":["tools","completion"],"model_info":{"llama.context_length":8192}}`)
	})

	meta, err := OllamaGetModelMetaData("llama3:8b")
	if err != nil {
		t.Fatalf("OllamaGetModelMetaData: %v", err)
	}
	if len(meta.Capabilities) != 2 || meta.Capabilities[0] != "tools" {
		t.Fatalf("unexpected capabilities: %v", meta.Capabilities)
	}
	if got := meta.ModelInfo["llama.context_length"]; got != float64(8192) {
		t.Fatalf("model_info context_length = %v, want 8192", got)
	}
}

func TestOllamaGetModelMetaDataNonOK(t *testing.T) {
	stubProvider(t, &ollamaClient, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	if _, err := OllamaGetModelMetaData("missing"); err == nil {
		t.Fatal("non-OK status should error")
	}
}

// ContextLength reads /api/ps's context_length for the exact running model
// name. A missing model or an /api/ps failure falls back to zero.
func TestOllamaContextLength(t *testing.T) {
	cases := []struct {
		name   string
		model  string
		status int
		body   string
		want   int
	}{
		{"matching model", "llama3:8b", http.StatusOK, `{"models":[{"name":"llama3:8b","context_length":8192}]}`, 8192},
		{"different model", "llama3:8b", http.StatusOK, `{"models":[{"name":"other:7b","context_length":4096}]}`, 0},
		{"empty list", "llama3:8b", http.StatusOK, `{"models":[]}`, 0},
		{"server error", "llama3:8b", http.StatusInternalServerError, "boom", 0},
		{"malformed body", "llama3:8b", http.StatusOK, "not json", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubProvider(t, &ollamaClient, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/ps" {
					t.Errorf("unexpected path %q", r.URL.Path)
				}

				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})

			got := (OllamaShowResponse{}).ContextLength(tc.model)
			if got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestOllamaLoadModel(t *testing.T) {
	stubProvider(t, &ollamaModelLoader, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %q, want /api/chat", r.URL.Path)
		}

		var payload struct {
			Model     string `json:"model"`
			Messages  []any  `json:"messages"`
			Stream    bool   `json:"stream"`
			KeepAlive string `json:"keep_alive"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}

		if payload.Model != "llama3:8b" {
			t.Errorf("model = %q, want llama3:8b", payload.Model)
		}
		if len(payload.Messages) != 0 {
			t.Errorf("messages = %d, want 0", len(payload.Messages))
		}
		if payload.Stream {
			t.Error("stream = true, want false")
		}
		if payload.KeepAlive != "5m" {
			t.Errorf("keep_alive = %q, want 5m", payload.KeepAlive)
		}

		fmt.Fprint(w, `{"done_reason":"load"}`)
	})

	if err := OllamaLoadModel("llama3:8b"); err != nil {
		t.Fatalf("OllamaLoadModel: %v", err)
	}
}

func TestOllamaLoadModelErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"json error status", http.StatusInternalServerError, `{"error":"model not found"}`, "model not found"},
		{"error field on success", http.StatusOK, `{"error":"failed to load model"}`, "failed to load model"},
		{"non-json error status", http.StatusBadGateway, "upstream unavailable", "upstream unavailable"},
		{"malformed success body", http.StatusOK, "not json", "decode ollama load response"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubProvider(t, &ollamaModelLoader, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})

			err := OllamaLoadModel("llama3:8b")
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------- vllm

func TestVLLMGetModelData(t *testing.T) {
	stubProvider(t, &vllmClient, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"meta-llama/Llama-3-8B"}]}`)
	})

	name, err := VLLMGetModelData()
	if err != nil {
		t.Fatalf("VLLMGetModelData: %v", err)
	}
	if name != "meta-llama/Llama-3-8B" {
		t.Fatalf("got %q", name)
	}
}

func TestVLLMGetModelDataNoModel(t *testing.T) {
	stubProvider(t, &vllmClient, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[]}`)
	})

	if _, err := VLLMGetModelData(); err == nil {
		t.Fatal("empty model list should error")
	}
}

func TestVLLMGetModelDataNeedsKey(t *testing.T) {
	stubProvider(t, &vllmClient, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	if _, err := VLLMGetModelData(); !errors.Is(err, ErrNeedsKey) {
		t.Fatalf("want ErrNeedsKey, got %v", err)
	}
}

func TestVLLMGetModelContextLength(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"top level", `{"data":[{"id":"m","max_model_len":32768}]}`, 32768},
		{"nested model_info", `{"data":[{"id":"m","model_info":{"max_model_len":16384}}]}`, 16384},
		{"unreported", `{"data":[{"id":"m"}]}`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubProvider(t, &vllmClient, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tc.body)
			})
			got, err := VLLMGetModelContextLength()
			if err != nil {
				t.Fatalf("VLLMGetModelContextLength: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestVLLMGetModelContextLengthNoModel(t *testing.T) {
	stubProvider(t, &vllmClient, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[]}`)
	})

	if _, err := VLLMGetModelContextLength(); err == nil {
		t.Fatal("no model loaded should error")
	}
}

// A 200 whose reply contains a structured call to the probe tool proves native
// tool calling works. A 200 with no tool_calls means the envelope fallback.
func TestVLLMRunSmokeTestNativeAndFallback(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"native tool call", `{"choices":[{"message":{"tool_calls":[{"function":{"name":"get_test_value"}}]}}]}`, true},
		{"no tool call", `{"choices":[{"message":{"content":"hello"}}]}`, false},
		{"empty choices", `{"choices":[]}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubProvider(t, &vllmClient, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tc.body)
			})
			got, err := VLLMRunSmokeTest("m")
			if err != nil {
				t.Fatalf("VLLMRunSmokeTest: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// The specific "tool calling not configured" 400 downgrades to the envelope
// path (false, nil), while any other 400 is a real error.
func TestVLLMRunSmokeTestBadRequests(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			"tool choice disabled",
			`{"error":{"message":"requires --enable-auto-tool-choice and --tool-call-parser"}}`,
			false,
		},
		{
			"unrelated 400",
			`{"error":{"message":"context length exceeded"}}`,
			true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubProvider(t, &vllmClient, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, tc.body)
			})
			ok, err := VLLMRunSmokeTest("m")
			if tc.wantErr && err == nil {
				t.Fatal("want error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("want downgrade with nil error, got %v", err)
			}
			if ok {
				t.Fatal("a 400 can never mean native tool calling")
			}
		})
	}
}

func TestVLLMRunSmokeTestNeedsKey(t *testing.T) {
	stubProvider(t, &vllmClient, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	if _, err := VLLMRunSmokeTest("m"); !errors.Is(err, ErrNeedsKey) {
		t.Fatalf("want ErrNeedsKey, got %v", err)
	}
}

func TestVLLMRunSmokeTestMalformedRequest(t *testing.T) {
	stubProvider(t, &vllmClient, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, "bad request")
	})

	if _, err := VLLMRunSmokeTest("m"); err == nil {
		t.Fatal("422 should error")
	}
}

func TestVLLMRunSmokeTestUnexpectedStatus(t *testing.T) {
	stubProvider(t, &vllmClient, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "boom")
	})

	if _, err := VLLMRunSmokeTest("m"); err == nil {
		t.Fatal("5xx should error, not silently downgrade")
	}
}
