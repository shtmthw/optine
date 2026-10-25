package harnessSession

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/mattthew/optine/internal/dataTypes"
)

// Kind identifies which provider-native message format a session holds.
// One session always lives on one loop: switching provider mid-session would
// mean translating history between formats, which this harness deliberately
// never does.
type Kind string

const (
	OllamaNative Kind = "ollama-native"
	VLLMNative   Kind = "vllm-native"
	Envelope     Kind = "envelope"
)

// DefaultContextLimit is used when the provider cannot report a model's
// context window. Conservative on purpose: local servers often run small
// windows.
const DefaultContextLimit = 8192

// Config describes the run a session is created for.
type Config struct {
	Provider     string
	Model        string
	Directory    string
	Kind         Kind
	ContextLimit int
	// MaxTurns is the envelope loop's call budget. It is needed to build the
	// envelope system prompt; native kinds ignore it.
	MaxTurns int
}

// Session owns one conversation's history plus its system-prompt cache.
//
// Single-writer rule: only the agent run goroutine appends to the slots. The
// TUI never reads them, and /resume swaps the whole session between runs.
type Session struct {
	UID       string
	Kind      Kind
	Provider  string
	Model     string
	Directory string
	CreatedAt time.Time
	UpdatedAt time.Time

	ContextLimit     int
	MaxTurns         int
	RunCount         int
	LastPromptTokens int

	SystemCache systemCache

	ollama   []*dataTypes.NativeTooltypeMessage
	vllm     []*dataTypes.VLLMTooltypeMessage
	envelope []*dataTypes.NonNativeMessage
}

// OnActiveSession is the UI bridge: the TUI sets it to display the session UID
// and receives updates when the active session changes. Nil-safe, mirroring
// internal/approval.
var OnActiveSession func(uid string)

// KindFor maps the provider + tool mode picked at startup onto the loop kind.
func KindFor(provider string, nativeToolCalling bool) Kind {
	if !nativeToolCalling {
		return Envelope
	}

	if provider == "vLLM" {
		return VLLMNative
	}

	return OllamaNative
}

// New builds an in-memory session with its system message seeded. It never
// touches the disk: the file appears on the first Commit, so quitting before
// the first message leaves no empty sessions behind.
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
	switch s.Kind {
	case OllamaNative:
		s.ollama = append(s.ollama, &dataTypes.NativeTooltypeMessage{Role: "user", Content: content})
	case VLLMNative:
		s.vllm = append(s.vllm, &dataTypes.VLLMTooltypeMessage{Role: "user", Content: content})
	case Envelope:
		s.envelope = append(s.envelope, &dataTypes.NonNativeMessage{Role: "user", Content: content})
	}
}

// The typed slot accessors return a pointer to the slice header, not a copy:
// append may reallocate the backing array, so every write has to go back into
// the session (the loops append through *slot for this reason).

func (s *Session) Ollama() *[]*dataTypes.NativeTooltypeMessage { return &s.ollama }

func (s *Session) VLLM() *[]*dataTypes.VLLMTooltypeMessage { return &s.vllm }

func (s *Session) Envelope() *[]*dataTypes.NonNativeMessage { return &s.envelope }

// RecordPromptTokens stores the provider's measured prompt size. Zero means
// the provider did not report it, so the last known value is kept.
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
	case VLLMNative:
		return len(s.vllm)
	case Envelope:
		return len(s.envelope)
	}

	return 0
}

// Resumeable reports whether this session can continue on the given loop.
func (s *Session) Resumeable(kind Kind) error {
	if s.Kind != kind {
		return fmt.Errorf("session %s is a %s session and cannot resume as %s", s.UID, s.Kind, kind)
	}

	return nil
}

// setSystemContent replaces ( or updates ) the system message in the active slot.
func (s *Session) setSystemContent(content string) {
	switch s.Kind {
	case OllamaNative:
		if len(s.ollama) == 0 || s.ollama[0].Role != "system" {
			s.ollama = append([]*dataTypes.NativeTooltypeMessage{{Role: "system"}}, s.ollama...)
		}
		s.ollama[0].Content = content
	case VLLMNative:
		if len(s.vllm) == 0 || s.vllm[0].Role != "system" {
			s.vllm = append([]*dataTypes.VLLMTooltypeMessage{{Role: "system"}}, s.vllm...)
		}
		s.vllm[0].Content = content
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
