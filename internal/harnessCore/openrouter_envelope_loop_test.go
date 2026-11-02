package harnessCore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattthew/optine/internal/dataTypes"
	"github.com/mattthew/optine/internal/harnessPermissions"
	"github.com/mattthew/optine/internal/harnessSession"
)

func stubOpenRouterAuth(t *testing.T, key string) {
	t.Helper()

	authPath := filepath.Join(t.TempDir(), "auth.json")
	cfg := dataTypes.AuthConfig{
		Connections: map[string]dataTypes.Connection{
			"openrouter": {APIKey: key},
		},
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal auth: %v", err)
	}
	if err := os.WriteFile(authPath, data, 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	old := harnessPermissions.AuthPath
	harnessPermissions.AuthPath = authPath
	t.Cleanup(func() { harnessPermissions.AuthPath = old })
}

func TestOpenRouterEnvelopeLoopToolCallRoundTrip(t *testing.T) {
	stubOpenRouterAuth(t, "secret")

	var (
		reqCount          int
		gotAuth, gotTitle bool
		gotJSONFormat     bool
		sawToolResult     bool
		firstRole         string
		firstContent      string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		gotAuth = true
		gotTitle = r.Header.Get("X-Title") == "optine"

		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages       []map[string]any `json:"messages"`
			ResponseFormat struct {
				Type string `json:"type"`
			} `json:"response_format"`
		}
		_ = json.Unmarshal(body, &req)
		gotJSONFormat = req.ResponseFormat.Type == "json_object"

		for i, m := range req.Messages {
			if i == 0 {
				firstRole, _ = m["role"].(string)
				firstContent, _ = m["content"].(string)
			}
			if content, _ := m["content"].(string); strings.HasPrefix(content, "TOOL_RESULT") {
				sawToolResult = true
			}
		}

		reqCount++
		w.Header().Set("Content-Type", "application/json")
		if reqCount == 1 {
			fmt.Fprint(w, vllmEnvelopeBody(envelopeToolCall))
			return
		}
		fmt.Fprint(w, vllmEnvelopeBody(envelopeFinalAnswer))
	}))
	t.Cleanup(srv.Close)

	old := openRouterChatURL
	openRouterChatURL = srv.URL
	t.Cleanup(func() { openRouterChatURL = old })

	sess := testSession(t, harnessSession.Envelope)
	sess.BeginRun()
	sess.AppendUser("hi")

	out, err := nonNativeAgentCall(context.Background(), sess, bufio.NewReader(strings.NewReader("")), "Openrouter", "test-model")
	if err != nil {
		t.Fatalf("loop: %v", err)
	}
	if out != "done" {
		t.Fatalf("want answer %q, got %q", "done", out)
	}
	if !gotAuth {
		t.Fatal("authorization header missing or wrong")
	}
	if !gotTitle {
		t.Fatal("X-Title header missing")
	}
	if !gotJSONFormat {
		t.Fatal("response_format json_object missing")
	}
	if !sawToolResult {
		t.Fatal("tool result not round-tripped to provider")
	}
	if reqCount != 2 {
		t.Fatalf("want 2 provider calls, got %d", reqCount)
	}
	if sess.LastPromptTokens != 1 {
		t.Fatalf("prompt tokens not recorded: %d", sess.LastPromptTokens)
	}
	if firstRole != "system" || strings.TrimSpace(firstContent) == "" {
		t.Fatalf("system prompt not prepended to the envelope request: role=%q", firstRole)
	}
	for i, msg := range *sess.Envelope() {
		if msg.Role == "system" {
			t.Fatalf("system prompt leaked into session history at index %d", i)
		}
	}
}

func TestOpenRouterEnvelopeLoopRequiresKey(t *testing.T) {
	authPath := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"connections":{}}`), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	old := harnessPermissions.AuthPath
	harnessPermissions.AuthPath = authPath
	t.Cleanup(func() { harnessPermissions.AuthPath = old })

	_, err := nonNativeAgentCall(context.Background(), testSession(t, harnessSession.Envelope), bufio.NewReader(strings.NewReader("")), "Openrouter", "test-model")
	if err == nil || !strings.Contains(err.Error(), "openrouter") {
		t.Fatalf("want missing-key error naming openrouter, got: %v", err)
	}
}

func TestOpenRouterEnvelopeLoopRateLimited(t *testing.T) {
	stubOpenRouterAuth(t, "secret")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"slow down"}}`)
	}))
	t.Cleanup(srv.Close)

	old := openRouterChatURL
	openRouterChatURL = srv.URL
	t.Cleanup(func() { openRouterChatURL = old })

	_, err := nonNativeAgentCall(context.Background(), testSession(t, harnessSession.Envelope), bufio.NewReader(strings.NewReader("")), "Openrouter", "test-model")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got: %v", err)
	}
	if !strings.Contains(err.Error(), "slow down") {
		t.Fatalf("want provider detail in error, got: %v", err)
	}
}
