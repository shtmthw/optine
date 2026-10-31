package harnessSession

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/mattthew/optine/internal/dataTypes"
)

// Kind identifies the provider-native message format a session holds. A
// session never switches format mid-run.
type Kind string

const (
	OllamaNative       Kind = "ollama-native"
	OpenAICompatNative Kind = "openAI-native"
	Envelope           Kind = "envelope"
)

// DefaultContextLimit is used when the provider reports no context window.
const DefaultContextLimit = 8192

// Config describes the run a session is created for.
type Config struct {
	Provider     string
	Model        string
	Directory    string
	Kind         Kind
	ContextLimit int
	MaxTurns     int
	// GitStatus selects the snapshot backend for a new session. Load restores
	// the saved value instead of consulting this field.
	GitStatus bool
}

// Session owns one conversation's history and system-prompt cache. Only the
// agent run goroutine appends; display code reads through Transcript().
type Session struct {
	UID       string
	Kind      Kind
	Provider  string
	Model     string
	Directory string
	CreatedAt time.Time
	UpdatedAt time.Time

	ContextLimit int
	MaxTurns     int
	// GitStatus is fixed when the session is created or loaded from disk and
	// selects the snapshot backend for the session's entire lifetime.
	GitStatus         bool
	RunCount          int
	TotalUserMessages int
	LastPromptTokens  int

	SystemCache systemCache

	ollama   []*dataTypes.NativeTooltypeMessage
	openAI   []*dataTypes.OpenAIChatMessage
	envelope []*dataTypes.NonNativeMessage
}

// OnActiveSession is the UI bridge for the active session UID.
var OnActiveSession func(uid string)

// KindFor maps the provider + tool mode picked at startup onto the loop kind.
func KindFor(provider string, nativeToolCalling bool) Kind {
	if !nativeToolCalling {
		return Envelope
	}

	if provider == "vLLM" || provider == "Openrouter" {
		return OpenAICompatNative
	}

	return OllamaNative
}

// New builds an in-memory session with its system message seeded. The file is
// written on first Commit, so quitting before the first message leaves nothing.
func New(cfg Config) *Session {
	limit := cfg.ContextLimit
	if limit <= 0 {
		limit = DefaultContextLimit
	}

	s := &Session{
		UID:          NewUID(),
		Kind:         cfg.Kind,
		Provider:     cfg.Provider,
		Model:        cfg.Model,
		Directory:    cfg.Directory,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		ContextLimit: limit,
		MaxTurns:     cfg.MaxTurns,
		GitStatus:    cfg.GitStatus,
	}

	s.SystemCache = newSystemCache(cfg.Kind, cfg.MaxTurns)
	s.setSystemContent(s.SystemCache.injected)

	return s
}

// BeginRun marks one user-message run. It is called from the run goroutine.
func (s *Session) BeginRun() {
	s.RunCount++
	s.UpdatedAt = time.Now()

	if OnActiveSession != nil {
		OnActiveSession(s.UID)
	}
}

// AppendUser appends the new user message to the active slot.
func (s *Session) AppendUser(content string) {
	s.TotalUserMessages++

	switch s.Kind {
	case OllamaNative:
		s.ollama = append(s.ollama, &dataTypes.NativeTooltypeMessage{Role: "user", Content: content})
	case OpenAICompatNative:
		s.openAI = append(s.openAI, &dataTypes.OpenAIChatMessage{Role: "user", Content: content})
	case Envelope:
		s.envelope = append(s.envelope, &dataTypes.NonNativeMessage{Role: "user", Content: content})
	}
}

// The typed slot accessors return a pointer to the slice header, not a copy:
// append may reallocate the backing array, so every write has to go back into
// the session (the loops append through *slot for this reason).

func (s *Session) Ollama() *[]*dataTypes.NativeTooltypeMessage { return &s.ollama }

func (s *Session) OpenAI() *[]*dataTypes.OpenAIChatMessage { return &s.openAI }

func (s *Session) Envelope() *[]*dataTypes.NonNativeMessage { return &s.envelope }

// RecordPromptTokens stores the provider's measured prompt size. Zero keeps
// the last known value.
func (s *Session) RecordPromptTokens(n int) {
	if n > 0 {
		s.LastPromptTokens = n
	}
}

// Len is the number of messages currently in the active slot.
func (s *Session) Len() int {
	switch s.Kind {
	case OllamaNative:
		return len(s.ollama)
	case OpenAICompatNative:
		return len(s.openAI)
	case Envelope:
		return len(s.envelope)
	}

	return 0
}

// Resumeable reports whether this session can continue on the given loop.
func (s *Session) Resumeable(kind Kind) error {
	if s.Kind != kind {
		return fmt.Errorf("session %s is of the %s protocol and cannot resume as %s", s.UID, s.Kind, kind)
	}

	return nil
}

// setSystemContent replaces or inserts the system message in the active slot.
func (s *Session) setSystemContent(content string) {
	switch s.Kind {
	case OllamaNative:
		if len(s.ollama) == 0 || s.ollama[0].Role != "system" {
			s.ollama = append([]*dataTypes.NativeTooltypeMessage{{Role: "system"}}, s.ollama...)
		}
		s.ollama[0].Content = content
	case OpenAICompatNative:
		if len(s.openAI) == 0 || s.openAI[0].Role != "system" {
			s.openAI = append([]*dataTypes.OpenAIChatMessage{{Role: "system"}}, s.openAI...)
		}
		s.openAI[0].Content = content
	case Envelope:
		if len(s.envelope) == 0 || s.envelope[0].Role != "system" {
			s.envelope = append([]*dataTypes.NonNativeMessage{{Role: "system"}}, s.envelope...)
		}
		s.envelope[0].Content = content
	}
}

// NewUID is timestamp-prefixed so a plain directory listing sorts by age, and
// random-suffixed so two sessions started in the same second never collide.
func NewUID() string {
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return time.Now().UTC().Format("20060102T150405Z")
	}

	return time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(suffix)
}
