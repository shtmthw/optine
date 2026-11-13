package slashCommands

import (
	"slices"
	"testing"
)

func names(commands []Command) []string {
	out := make([]string, len(commands))
	for i, c := range commands {
		out[i] = c.Name
	}

	return out
}

// Session-starting commands exist only in the launcher, session-only
// commands only inside a session, and both session scopes offer the same
// commands with scope-specific wording.
func TestScopesOfferTheRightCommands(t *testing.T) {
	launcher := names(For(Launcher))
	local := names(For(LocalSession))
	remote := names(For(RemoteSession))

	for _, name := range []string{"/local", "/remote", "/connect"} {
		if !slices.Contains(launcher, name) {
			t.Fatalf("launcher lacks %s", name)
		}
		if slices.Contains(local, name) || slices.Contains(remote, name) {
			t.Fatalf("%s must not be offered inside a session", name)
		}
	}

	for _, name := range []string{"/model", "/new", "/revert", "/redo", "/rewind"} {
		if slices.Contains(launcher, name) {
			t.Fatalf("%s needs a session and must not be offered in the launcher", name)
		}
		if !slices.Contains(local, name) {
			t.Fatalf("sessions lack %s", name)
		}
	}

	if !slices.Equal(local, remote) {
		t.Fatalf("local %v and remote %v sessions should offer the same commands", local, remote)
	}

	model := func(scope Scope) string {
		c, _ := Lookup(For(scope), "/model")
		return c.Description
	}
	if model(LocalSession) == model(RemoteSession) {
		t.Fatal("/model should describe the local-only restriction differently from remote")
	}

	for _, scope := range []Scope{Launcher, LocalSession, RemoteSession} {
		for _, name := range []string{"/help", "/quit"} {
			if _, ok := Lookup(For(scope), name); !ok {
				t.Fatalf("scope %d lacks %s", scope, name)
			}
		}
	}
}

func TestLookupMatchesNamesAndAliases(t *testing.T) {
	commands := For(Launcher)

	for input, want := range map[string]string{
		"/quit":   "/quit",
		"/q":      "/quit",
		" /exit ": "/quit",
		"/h":      "/help",
		"/local":  "/local",
	} {
		c, ok := Lookup(commands, input)
		if !ok || c.Name != want {
			t.Fatalf("Lookup(%q) = %q, %v; want %q", input, c.Name, ok, want)
		}
	}

	for _, input := range []string{"/lo", "local", "/local now", ""} {
		if c, ok := Lookup(commands, input); ok {
			t.Fatalf("Lookup(%q) matched %q", input, c.Name)
		}
	}
}

func TestMatch(t *testing.T) {
	commands := For(Launcher)

	if got := names(Match(commands, "/")); !slices.Equal(got, names(commands)) {
		t.Fatalf("a bare / should list every command in order, got %v", got)
	}

	if got := names(Match(commands, "/re")); !slices.Equal(got, []string{"/remote", "/resume"}) {
		t.Fatalf("/re = %v", got)
	}

	// A one-letter query matches names only, not every description.
	if got := names(Match(commands, "/r")); !slices.Equal(got, []string{"/remote", "/resume"}) {
		t.Fatalf("/r = %v", got)
	}

	// Aliases count as prefixes; longer queries also search descriptions.
	if got := names(Match(commands, "/ex")); !slices.Equal(got, []string{"/quit"}) {
		t.Fatalf("/ex = %v", got)
	}
	if got := names(Match(commands, "/key")); !slices.Equal(got, []string{"/connect"}) {
		t.Fatalf("/key = %v", got)
	}

	for _, input := range []string{"", "local", "/local now", "/zzz"} {
		if got := Match(commands, input); len(got) != 0 {
			t.Fatalf("Match(%q) = %v, want nothing", input, names(got))
		}
	}
}
