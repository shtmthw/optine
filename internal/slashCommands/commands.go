// Package slashCommands is the single list of slash commands optine accepts.
// The launcher prompt, the TUI command palette and /help all read from it, so
// a command's name and description cannot drift between them.
package slashCommands

import (
	"slices"
	"strings"
)

// Scope says where a command list is used.
type Scope int

const (
	// Launcher is the startup prompt, before any session exists.
	Launcher Scope = iota
	// LocalSession is the agent interface of a /local session, which can
	// only use local models (Ollama, vLLM).
	LocalSession
	// RemoteSession is the agent interface of a /remote session, which can
	// use hosted OpenRouter models as well as local ones.
	RemoteSession
)

// Command is one slash command as shown to the user.
type Command struct {
	Name        string   // canonical spelling, e.g. "/model"
	Aliases     []string // other accepted spellings, e.g. "/q"
	Description string   // one short line
}

var (
	help    = Command{Name: "/help", Aliases: []string{"/h"}, Description: "List the commands available here"}
	funfact = Command{Name: "/funfact", Description: "A fact about who built optine"}
	quit    = Command{Name: "/quit", Aliases: []string{"/exit", "/q"}, Description: "Exit optine"}

	newSession  = Command{Name: "/new", Description: "Start a new, empty session in this directory"}
	rewind      = Command{Name: "/rewind", Description: "Take the conversation and files back to before one of your messages"}
	sessionQuit = Command{Name: "/quit", Aliases: []string{"/exit", "/q"}, Description: "Exit optine; ctrl+c returns to the launcher instead"}
)

// For returns the commands available in scope, in display order.
func For(scope Scope) []Command {
	switch scope {
	case LocalSession:
		return []Command{
			{Name: "/model", Description: "Switch to another local model (Ollama or vLLM)"},
			newSession,
			{Name: "/resume", Description: "Reopen a saved local session"},
			{Name: "/revert", Description: "Restore the files changed by the latest request"},
			{Name: "/redo", Description: "Put back the changes the last /revert undid"},
			rewind,
			help, funfact, sessionQuit,
		}

	case RemoteSession:
		return []Command{
			{Name: "/model", Description: "Switch model: OpenRouter, Ollama or vLLM"},
			newSession,
			{Name: "/resume", Description: "Reopen a saved remote session"},
			{Name: "/revert", Description: "Restore the files changed by the latest request"},
			{Name: "/redo", Description: "Put back the changes the last /revert undid"},
			rewind,
			help, funfact, sessionQuit,
		}

	default:
		return []Command{
			{Name: "/local", Description: "Start a session on a local model (Ollama or vLLM)"},
			{Name: "/remote", Description: "Start a session on a hosted model through OpenRouter"},
			{Name: "/connect", Description: "Save an API key (OpenRouter, Tavily, OpenAI, Anthropic)"},
			{Name: "/resume", Description: "Resume the latest session saved in this directory"},
			help, funfact, quit,
		}
	}
}

// Spellings returns the name followed by every alias.
func (c Command) Spellings() []string {
	return append([]string{c.Name}, c.Aliases...)
}

// Lookup finds the command whose name or alias is exactly input, ignoring
// surrounding whitespace.
func Lookup(commands []Command, input string) (Command, bool) {
	input = strings.TrimSpace(input)

	for _, c := range commands {
		if slices.Contains(c.Spellings(), input) {
			return c, true
		}
	}

	return Command{}, false
}

// Match returns the commands to suggest while the user types input. Only a
// lone word starting with "/" gets suggestions. Commands whose name or alias
// starts with the input come first; then commands whose name contains it,
// or, once the query is long enough to be meaningful, whose description
// does.
func Match(commands []Command, input string) []Command {
	if !strings.HasPrefix(input, "/") || strings.ContainsAny(input, " \t") {
		return nil
	}

	query := strings.ToLower(input)

	var prefixed, contained []Command

	for _, c := range commands {
		if hasPrefixSpelling(c, query) {
			prefixed = append(prefixed, c)
			continue
		}

		needle := strings.TrimPrefix(query, "/")
		if needle == "" {
			continue
		}

		if strings.Contains(strings.ToLower(c.Name), needle) ||
			(len(needle) >= minDescriptionQuery && strings.Contains(strings.ToLower(c.Description), needle)) {
			contained = append(contained, c)
		}
	}

	return append(prefixed, contained...)
}

// minDescriptionQuery is the shortest query matched against descriptions;
// one or two letters match almost every description.
const minDescriptionQuery = 3

func hasPrefixSpelling(c Command, query string) bool {
	for _, s := range c.Spellings() {
		if strings.HasPrefix(strings.ToLower(s), query) {
			return true
		}
	}

	return false
}
