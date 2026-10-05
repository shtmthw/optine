package cli

import (
	"bufio"
	"io"
	"log"
	"strings"
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
		// the provider selection
		if err := selectProvider(reader); err != nil {
			log.Println(err)
		}
		return false

	case "/help", "/h":
		log.Println("available commands: /local (pick provider and start), /funfact, /help, /quit")
		return false

	case "/quit", "/exit", "/q":
		return true

	case "/funfact":
		log.Println("this is a one man built agent, and that man is Matthew Baroi")
		return false

	default:
		log.Println("unknown command, type /help for the list")
		return false
	}
}
