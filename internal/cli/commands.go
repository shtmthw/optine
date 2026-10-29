package cli

import (
	"bufio"
	"io"
	"log"
	"os"
	"strings"

	"github.com/mattthew/optine/internal/harnessSession"
)

// RunCommand handles one slash command. It reports whether the caller should
// exit: true on /quit or on EOF, false otherwise. Anything that is not a
// slash command or an unknown command keeps the caller in its loop.
func RunCommand(explicitInput string, externalInput bool, reader *bufio.Reader) bool {
	var command string

	if externalInput {
		command = explicitInput
	} else {
		input, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				log.Println(err)
			}
			return true
		}

		command = input
	}

	command = strings.TrimSpace(command)

	if command == "" {
		return false
	}

	if !strings.HasPrefix(command, "/") {
		log.Println("Not a command that optine supports, type /help for the list")
		return false
	}
	switch command {
	case "/local":
		if externalInput {
			log.Println("not allowed to run /local while in the agent interface")
			return false
		}
		log.Println("starting /local execution")
		if err := selectProvider(reader); err != nil {
			log.Println("RunCommand error: ", err)
		}
		return false

	case "/connect":
		if externalInput {
			log.Println("not allowed to run /connect while in the agent interface")
			return false
		}
		log.Println("starting /connect execution")
		if err := selectConnection(reader); err != nil {
			log.Println("RunCommand error: ", err)
		}
		return false

	case "/help", "/h":
		log.Println("available commands: /local (pick provider and start), /resume (pick session and resume work), /remote (under development), /connect (connect to supported api providers), /funfact, /help, /quit")
		return false

	case "/quit", "/exit", "/q":
		return true

	case "/funfact":
		log.Println("this is a one man built agent, and that man is Matthew Baroi")
		return false
	case "/resume":
		cwd, err := os.Getwd()
		if err != nil {
			log.Printf("error occurred while trying to get the CWD of user, err: %v", err)
			return false
		}
		availableSess, listErr := harnessSession.List(cwd, true)

		if listErr != nil {
			log.Printf("error occurred while listing the available sessions, err: %v", listErr)
			return false
		}

		if len(availableSess) == 0 {
			log.Println("No sessions related this directory, please run /local or /remote to start a new session")
			return false
		}

		providerErr := checkProviderAvailability(availableSess[0].Provider, availableSess[0].Model)

		if providerErr != nil {
			log.Printf("Error: %v", providerErr)
			return false
		}
		isNativeToolSupportive := true

		if availableSess[0].Kind == "envelope" {
			isNativeToolSupportive = false
		}

		log.Println("Resuming the last session related to this directory...")

		if err := runAgentSession(availableSess[0].Provider, availableSess[0].Model, isNativeToolSupportive, availableSess[0].ContextLimit, availableSess[0].UID, true); err != nil {
			log.Printf("error occurred while trying to initiate the agent, err: %v", err)
			return false
		}

		return false

	default:
		log.Println("unknown command, type /help for the list")
		return false
	}
}
