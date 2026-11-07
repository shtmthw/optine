package cli

import (
	"bufio"
	"errors"
	"io"
	"log"
	"os"
	"strings"

	"github.com/mattthew/optine/internal/harnessSession"
	"github.com/mattthew/optine/internal/tui"
)

// RunCommand handles one slash command. It reports whether the caller should
// exit: true on /quit or on EOF, false otherwise. Anything that is not a
// slash command or an unknown command keeps the caller in its loop.
//
// externalInput means the command was typed inside the agent interface and
// is passed in explicitInput; otherwise one line is read from reader.
func RunCommand(explicitInput string, externalInput bool, reader *bufio.Reader) bool {
	command := explicitInput

	if !externalInput {
		input, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				log.Println(err)
			}
			return true
		}

		command = input
	}

	return dispatch(command, externalInput, reader)
}

// dispatch runs one command line. inSession is true when it was typed inside
// the agent interface, where commands that start a new session are refused
// and output goes to the interface instead of the terminal.
func dispatch(command string, inSession bool, reader *bufio.Reader) bool {
	out := printer{inSession: inSession}

	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}

	if !strings.HasPrefix(command, "/") {
		out.warn("%q is not a command; type / to pick one or /help to list them", command)
		return false
	}

	switch command {
	case "/local", "/remote", "/connect":
		if inSession {
			out.warn("%s is a launcher command: press ctrl+c to leave this session and return to the launcher", command)
			return false
		}

		var err error
		switch command {
		case "/local":
			err = selectProvider(reader)
		case "/remote":
			err = filterModelProviderAvailability(reader)
		case "/connect":
			err = selectConnection(reader)
		}

		return sessionEnded(out, err)

	case "/help", "/h":
		out.help()
		return false

	case "/quit", "/exit", "/q":
		return true

	case "/funfact":
		out.info("optine is a one-person project, built by Matthew Baroi")
		return false

	case "/resume":
		return sessionEnded(out, resumeLatest(out))

	default:
		out.warn("unknown command %s; type / to pick one or /help to list them", command)
		return false
	}
}

// sessionEnded reports whether err means optine should exit (the user typed
// /quit inside the agent interface) and prints any other error once.
func sessionEnded(out printer, err error) bool {
	switch {
	case err == nil, errors.Is(err, errCancelled):
		return false

	case errors.Is(err, tui.ErrQuit):
		return true
	}

	out.fail("%v", err)
	return false
}

// resumeLatest resumes the newest session saved for the working directory.
// Inside the agent interface /resume opens a picker instead; this is the
// launcher's version.
func resumeLatest(out printer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return errors.New("cannot read the working directory: " + err.Error())
	}

	sessions, err := harnessSession.List(cwd, true)
	if err != nil {
		return errors.New("cannot list saved sessions: " + err.Error())
	}

	if len(sessions) == 0 {
		out.warn("no saved sessions for %s; start one with /local or /remote", cwd)
		return nil
	}

	latest := sessions[0]

	if err := checkProviderAvailability(latest.Provider, latest.Model); err != nil {
		return err
	}

	out.ok("resuming session %s (%s · %s, %d messages)", latest.UID, latest.Provider, latest.Model, latest.TotalUserMessages)

	return RunAgentSession(latest.Provider, latest.Model, latest.ToolCall, latest.ContextLimit, latest.UID, true, false)
}
