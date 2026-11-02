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

	"github.com/mattthew/optine/internal/harnessPermissions"
)

// resolveSessionsDir is a var so tests can redirect persistence to a temp dir.
var resolveSessionsDir = defaultSessionsDir

// defaultSessionsDir derives the sessions directory from AuthPath's parent.
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

// sessionFile is the on-disk shape. Messages holds the whole conversation in
// provider-neutral form; the system prompt is stored at top level.
type sessionFile struct {
	UID               string    `json:"uid"`
	Kind              Kind      `json:"kind"`
	Provider          string    `json:"provider"`
	Model             string    `json:"model"`
	Directory         string    `json:"directory,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	ContextLimit      int       `json:"context_limit,omitempty"`
	MaxTurns          int       `json:"max_turns,omitempty"`
	GitStatus         bool      `json:"git_status,omitempty"`
	LocalOnly         bool      `json:"local_only,omitempty"`
	ToolCall          bool      `json:"tool_call,omitempty"`
	RunCount          int       `json:"run_count"`
	TotalUserMessages int       `json:"total_user_messages"`
	LastPromptTokens  int       `json:"last_prompt_tokens,omitempty"`
	SystemPrompt      string    `json:"system_prompt,omitempty"`
	Messages          []Message `json:"messages,omitempty"`
}

func (f sessionFile) messageLen() int {
	return len(f.Messages)
}

func toFile(s *Session) sessionFile {
	f := sessionFile{
		UID:               s.UID,
		Kind:              s.Kind,
		Provider:          s.Provider,
		Model:             s.Model,
		Directory:         s.Directory,
		CreatedAt:         s.CreatedAt,
		UpdatedAt:         s.UpdatedAt,
		ContextLimit:      s.ContextLimit,
		MaxTurns:          s.MaxTurns,
		GitStatus:         s.GitStatus,
		LocalOnly:         s.LocalOnly,
		ToolCall:          s.ToolCall,
		RunCount:          s.RunCount,
		TotalUserMessages: s.TotalUserMessages,
		LastPromptTokens:  s.LastPromptTokens,
		SystemPrompt:      s.SystemPrompt,
		Messages:          s.Messages,
	}

	return f
}

// Commit persists the session after a run. Sessions that never ran are skipped.
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
	UID               string
	Provider          string
	Model             string
	Directory         string
	Kind              Kind
	UpdatedAt         time.Time
	Messages          int
	TotalUserMessages int
	ContextLimit      int
	GitStatus         bool
	LocalOnly         bool
	ToolCall          bool
}

func loopInternals(
	sessionsDir string,
	optionalDir string,
	filterByDir bool,
	entries []os.DirEntry,
	metas *[]Meta,
) {
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(sessionsDir, entry.Name()))
		if err != nil {
			continue
		}

		var f sessionFile
		if err := json.Unmarshal(data, &f); err != nil {
			continue
		}

		if filterByDir && f.Directory != optionalDir {
			continue
		}

		*metas = append(*metas, Meta{
			UID:               f.UID,
			Provider:          f.Provider,
			Model:             f.Model,
			Directory:         f.Directory,
			Kind:              f.Kind,
			UpdatedAt:         f.UpdatedAt,
			Messages:          f.messageLen(),
			TotalUserMessages: f.TotalUserMessages,
			ContextLimit:      f.ContextLimit,
			GitStatus:         f.GitStatus,
			LocalOnly:         f.LocalOnly,
			ToolCall:          f.ToolCall,
		})
	}
}

// List returns saved sessions newest-first.
func List(optionalDir string, isOptionalDirAvailable bool) ([]Meta, error) {

	dir, err := resolveSessionsDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	metas := make([]Meta, 0, len(entries))

	loopInternals(dir, optionalDir, isOptionalDirAvailable, entries, &metas)
	sort.Slice(metas, func(i, j int) bool {
		return metas[i].UpdatedAt.After(metas[j].UpdatedAt)
	})
	return metas, nil
}

// Load restores a session by UID and refreshes its system message.
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
		UID:               uid,
		Kind:              f.Kind,
		Provider:          f.Provider,
		Model:             f.Model,
		Directory:         f.Directory,
		CreatedAt:         f.CreatedAt,
		UpdatedAt:         f.UpdatedAt,
		ContextLimit:      f.ContextLimit,
		MaxTurns:          f.MaxTurns,
		GitStatus:         f.GitStatus,
		LocalOnly:         f.LocalOnly,
		ToolCall:          f.ToolCall,
		RunCount:          f.RunCount,
		TotalUserMessages: f.TotalUserMessages,
		LastPromptTokens:  f.LastPromptTokens,
		SystemPrompt:      f.SystemPrompt,
	}

	// History is provider-neutral, so it survives a Kind change on load.
	s.Messages = f.Messages

	if err := validateToolPairs(s.Messages); err != nil {
		return nil, fmt.Errorf("session %s is corrupt: %w", uid, err)
	}

	if s.ContextLimit <= 0 {
		s.ContextLimit = DefaultContextLimit
	}

	s.restoreSystemCache()

	s.ReconcileKind()

	return s, nil
}

// restoreSystemCache seeds the prompt cache from a loaded session.
func (s *Session) restoreSystemCache() {
	if s.SystemPrompt == "" {
		s.SystemCache = newSystemCache(s.MaxTurns, s.ToolCall)
		s.SystemPrompt = s.SystemCache.compose()
		return
	}

	// The prompt date tracks the session, so reuse its creation time.
	promptTime := s.CreatedAt

	// The base is rebuilt for the session's tool mode.
	s.SystemCache = systemCache{
		promptTime: promptTime,
		maxTurns:   s.MaxTurns,
	}
	s.SystemCache.refreshBase(s.ToolCall, availableCrawlers())
}

// validateToolPairs rejects an assistant tool-call message whose tool replies are missing.
func validateToolPairs(msgs []Message) error {
	for i, msg := range msgs {
		if len(msg.ToolCalls) == 0 {
			continue
		}

		missing := make(map[string]bool, len(msg.ToolCalls))
		for _, call := range msg.ToolCalls {
			if call.ID != "" {
				missing[call.ID] = true
			}
		}

		replies := 0
		for j := i + 1; j < len(msgs) && msgs[j].Role == "tool"; j++ {
			replies++
			delete(missing, msgs[j].ToolCallID)
		}

		if replies < len(msg.ToolCalls) || len(missing) > 0 {
			return fmt.Errorf("assistant tool call at %d has %d of %d results", i, replies, len(msg.ToolCalls))
		}
	}

	return nil
}
