package harnessSession

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mattthew/optine/internal/dataTypes"
	"github.com/mattthew/optine/internal/harnessPermissions"
)

// resolveSessionsDir is a var so tests can redirect persistence to a temp dir.
var resolveSessionsDir = defaultSessionsDir

// defaultSessionsDir derives the sessions directory from AuthPath's parent.
// Per internal/harnessPermissions/optineConfigDir.go, every config-dir file is
// derived from AuthPath; no other package resolves os.UserConfigDir itself.
func defaultSessionsDir() (string, error) {
	if harnessPermissions.AuthPath == "" {
		return "", errors.New("optine config dir unavailable")
	}

	dir := filepath.Join(filepath.Dir(harnessPermissions.AuthPath), "sessions")

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}

	return dir, nil
}

// sessionFile is the on-disk shape. Exactly one message array is populated,
// matching Kind, so history is replayed exactly as the server sent it.
type sessionFile struct {
	UID              string                             `json:"uid"`
	Kind             Kind                               `json:"kind"`
	Provider         string                             `json:"provider"`
	Model            string                             `json:"model"`
	Directory        string                             `json:"directory,omitempty"`
	CreatedAt        time.Time                          `json:"created_at"`
	UpdatedAt        time.Time                          `json:"updated_at"`
	ContextLimit     int                                `json:"context_limit,omitempty"`
	MaxTurns         int                                `json:"max_turns,omitempty"`
	RunCount         int                                `json:"run_count"`
	LastPromptTokens int                                `json:"last_prompt_tokens,omitempty"`
	Ollama           []*dataTypes.NativeTooltypeMessage `json:"ollama_messages,omitempty"`
	VLLM             []*dataTypes.VLLMTooltypeMessage   `json:"vllm_messages,omitempty"`
	Envelope         []*dataTypes.NonNativeMessage      `json:"envelope_messages,omitempty"`
}

func (f sessionFile) messageLen() int {
	switch f.Kind {
	case OllamaNative:
		return len(f.Ollama)
	case VLLMNative:
		return len(f.VLLM)
	case Envelope:
		return len(f.Envelope)
	}

	return 0
}

func toFile(s *Session) sessionFile {
	f := sessionFile{
		UID:              s.UID,
		Kind:             s.Kind,
		Provider:         s.Provider,
		Model:            s.Model,
		Directory:        s.Directory,
		CreatedAt:        s.CreatedAt,
		UpdatedAt:        s.UpdatedAt,
		ContextLimit:     s.ContextLimit,
		MaxTurns:         s.MaxTurns,
		RunCount:         s.RunCount,
		LastPromptTokens: s.LastPromptTokens,
	}

	switch s.Kind {
	case OllamaNative:
		f.Ollama = s.ollama
	case VLLMNative:
		f.VLLM = s.vllm
	case Envelope:
		f.Envelope = s.envelope
	}

	return f
}

// Commit persists the session after a run. Best-effort: the caller logs
// failures, and a bad disk never aborts the agent loop. Sessions that never
// ran are skipped so /local without a message leaves no files behind.
func Commit(s *Session) error {
	if s == nil || s.RunCount == 0 {
		return nil
	}

	dir, err := resolveSessionsDir()
	if err != nil {
		return err
	}

	s.UpdatedAt = time.Now()

	data, err := json.MarshalIndent(toFile(s), "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".session-*.tmp")
	if err != nil {
		return err
	}

	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}

	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}

	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmpPath, filepath.Join(dir, s.UID+".json"))
}

// Meta is the header summary /resume shows without loading full histories.
type Meta struct {
	UID       string
	Provider  string
	Model     string
	Directory string
	Kind      Kind
	UpdatedAt time.Time
	Messages  int
}

// List returns saved sessions newest-first.
func List() ([]Meta, error) {
	dir, err := resolveSessionsDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	metas := make([]Meta, 0, len(entries))

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}

		var f sessionFile
		if err := json.Unmarshal(data, &f); err != nil {
			continue
		}

		metas = append(metas, Meta{
			UID:       f.UID,
			Provider:  f.Provider,
			Model:     f.Model,
			Directory: f.Directory,
			Kind:      f.Kind,
			UpdatedAt: f.UpdatedAt,
			Messages:  f.messageLen(),
		})
	}

	sort.Slice(metas, func(i, j int) bool {
		return metas[i].UpdatedAt.After(metas[j].UpdatedAt)
	})

	return metas, nil
}

// Load restores a session by UID. The system message is refreshed from the
// current notebook/crawler state on load, so resumed sessions never serve a
// stale memory snapshot.
func Load(uid string) (*Session, error) {
	if uid == "" || uid != filepath.Base(uid) || strings.ContainsAny(uid, `/\`) {
		return nil, fmt.Errorf("invalid session id %q", uid)
	}

	dir, err := resolveSessionsDir()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(filepath.Join(dir, uid+".json"))
	if err != nil {
		return nil, err
	}

	var f sessionFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("decoding session %s: %w", uid, err)
	}

	s := &Session{
		UID:              uid,
		Kind:             f.Kind,
		Provider:         f.Provider,
		Model:            f.Model,
		Directory:        f.Directory,
		CreatedAt:        f.CreatedAt,
		UpdatedAt:        f.UpdatedAt,
		ContextLimit:     f.ContextLimit,
		MaxTurns:         f.MaxTurns,
		RunCount:         f.RunCount,
		LastPromptTokens: f.LastPromptTokens,
	}

	if s.ContextLimit <= 0 {
		s.ContextLimit = DefaultContextLimit
	}

	switch f.Kind {
	case OllamaNative:
		s.ollama = f.Ollama
		if err := validateOllamaPairs(s.ollama); err != nil {
			return nil, fmt.Errorf("session %s is corrupt: %w", uid, err)
		}
	case VLLMNative:
		s.vllm = f.VLLM
		if err := validateVLLMPairs(s.vllm); err != nil {
			return nil, fmt.Errorf("session %s is corrupt: %w", uid, err)
		}
	case Envelope:
		s.envelope = f.Envelope
	default:
		return nil, fmt.Errorf("session %s has unknown kind %q", uid, f.Kind)
	}

	if s.Len() == 0 {
		return nil, fmt.Errorf("session %s is empty", uid)
	}

	s.SystemCache = newSystemCache(s.Kind, s.MaxTurns)
	s.setSystemContent(s.SystemCache.injected)

	return s, nil
}

// validateOllamaPairs rejects a history where an assistant tool-call message
// has fewer following tool replies than calls. Ollama ignores call ids, so
// only the count is enforced.
func validateOllamaPairs(msgs []*dataTypes.NativeTooltypeMessage) error {
	for i, msg := range msgs {
		if msg == nil {
			return errors.New("nil message")
		}

		if len(msg.ToolCalls) == 0 {
			continue
		}

		replies := 0
		for j := i + 1; j < len(msgs) && msgs[j].Role == "tool"; j++ {
			replies++
		}

		if replies < len(msg.ToolCalls) {
			return fmt.Errorf("assistant tool call at %d has %d of %d results", i, replies, len(msg.ToolCalls))
		}
	}

	return nil
}

// validateVLLMPairs is the vLLM counterpart. OpenAI-compatible servers match
// tool replies by id, so every call id must have its reply.
func validateVLLMPairs(msgs []*dataTypes.VLLMTooltypeMessage) error {
	for i, msg := range msgs {
		if msg == nil {
			return errors.New("nil message")
		}

		if len(msg.ToolCalls) == 0 {
			continue
		}

		missing := make(map[string]bool, len(msg.ToolCalls))
		for _, call := range msg.ToolCalls {
			if call.ID != "" {
				missing[call.ID] = true
			}
		}

		for j := i + 1; j < len(msgs) && msgs[j].Role == "tool"; j++ {
			delete(missing, msgs[j].ToolCallID)
		}

		if len(missing) > 0 {
			return fmt.Errorf("assistant tool call at %d is missing %d tool result(s)", i, len(missing))
		}
	}

	return nil
}
