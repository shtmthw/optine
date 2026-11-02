package harnessSession

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/mattthew/optine/internal/dataTypes"
)

// Kind identifies the provider-native message format a session holds. A
// session never switches format mid-run.
type Kind string

const (
	OllamaNative          Kind = "ollama-native"
	OpenAIChatCompletions Kind = "openai-chat-completions"
	OpenAICompatNative    Kind = OpenAIChatCompletions // alias: vLLM/OpenRouter use openai-chat-completions
	OpenAIResponses       Kind = "openai-responses"
	AnthropicMessages     Kind = "anthropic-messages"
	GeminiGenerateContent Kind = "gemini-generate-content"

	Envelope Kind = "envelope"
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
	// LocalOnly marks /local sessions, which must never switch to remote
	// (OpenRouter) models via /model. Set once at New, preserved on Load,
	// never rewritten afterward.
	LocalOnly bool
	// ToolCall selects native tool calling for a new session. Load restores the
	// saved value instead of consulting this field.
	ToolCall bool
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
	LocalOnly         bool
	ToolCall          bool
	RunCount          int
	TotalUserMessages int
	LastPromptTokens  int

	SystemPrompt string

	SystemCache systemCache

	ollamaNative          []*dataTypes.NativeTooltypeMessage
	openAIChatCompletions []*dataTypes.OpenAIChatMessage
	envelope              []*dataTypes.NonNativeMessage
}

// OnActiveSession is the UI bridge for the active session UID.
var OnActiveSession func(uid string)

// KindFor maps the provider + tool mode picked at startup onto the loop kind.
func KindFor(provider string, nativeToolCalling bool) Kind {
	if !nativeToolCalling {
		return Envelope
	}

	if strings.EqualFold(provider, "vLLM") || strings.EqualFold(provider, "Openrouter") {
		return OpenAICompatNative
	}

	return OllamaNative
}

// New builds an in-memory session with its system prompt seeded. The file is
// written on first Commit, so quitting before the first message leaves nothing.
func New(cfg Config) *Session {
	limit := cfg.ContextLimit
	if limit <= 0 {
		limit = DefaultContextLimit
	}

	now := time.Now()
	s := &Session{
		UID:          NewUID(),
		Kind:         cfg.Kind,
		Provider:     cfg.Provider,
		Model:        cfg.Model,
		Directory:    cfg.Directory,
		CreatedAt:    now,
		UpdatedAt:    now,
		ContextLimit: limit,
		MaxTurns:     cfg.MaxTurns,
		GitStatus:    cfg.GitStatus,
		LocalOnly:    cfg.LocalOnly,
		ToolCall:     cfg.ToolCall,
	}

	s.SystemCache = newSystemCache(cfg.MaxTurns, cfg.ToolCall)
	s.SystemPrompt = s.SystemCache.compose()

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
		s.ollamaNative = append(s.ollamaNative, &dataTypes.NativeTooltypeMessage{Role: "user", Content: content})
	case OpenAICompatNative:
		s.openAIChatCompletions = append(s.openAIChatCompletions, &dataTypes.OpenAIChatMessage{Role: "user", Content: content})
	case Envelope:
		s.envelope = append(s.envelope, &dataTypes.NonNativeMessage{Role: "user", Content: content})
	}
}

// The typed slot accessors return a pointer to the slice header, not a copy:
// append may reallocate the backing array, so every write has to go back into
// the session (the loops append through *slot for this reason).

func (s *Session) OllamaNative() *[]*dataTypes.NativeTooltypeMessage { return &s.ollamaNative }

func (s *Session) OpenAIChatCompletions() *[]*dataTypes.OpenAIChatMessage {
	return &s.openAIChatCompletions
}

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
		return len(s.ollamaNative)
	case OpenAICompatNative:
		return len(s.openAIChatCompletions)
	case Envelope:
		return len(s.envelope)
	}

	return 0
}

// Resumeable reports whether this session can continue on the given loop.

func (s *Session) Resumeable(kind Kind) error {
	return nil
}

// ReconcileKind restores the invariant sess.Kind == KindFor(sess.Provider, nativeToolCalling) when a saved session holds a decoupled kind/provider pair
// for example kind=ollama-native with provider=Openrouter.
func (s *Session) ReconcileKind() {
	nativeToolCalling := s.Kind != Envelope
	s.ToolCall = nativeToolCalling
	expected := KindFor(s.Provider, nativeToolCalling)

	if s.Kind == expected {
		return
	}

	s.Kind = expected
	s.SystemCache = newSystemCache(s.MaxTurns, s.ToolCall)
	s.SystemPrompt = s.SystemCache.compose()
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
