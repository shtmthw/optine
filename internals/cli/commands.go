package cli

import (
	"bufio"
	"log"
	"strings"
)

// taking in the case for /local
func RunCommand(explicitInput string, externalInput bool, reader *bufio.Reader) {
	var command string

	if externalInput {
		command = explicitInput
	} else {
		input, err := reader.ReadString('\n')
		if err != nil {
			return
		}

		command = input
	}

	command = strings.TrimSpace(command)

	if !strings.HasPrefix(command, "/") {
		log.Println("Not a command that optine supports")
		return
	}

	switch command {
	case "/local":
		if externalInput {
			log.Println("not allowed to run /local while in the agent interface")
			return
		}
		log.Println("starting /local execution")
		// the provider selection
		if err := selectProvider(reader); err != nil {
			log.Println(err)
		}

	case "/funfact":
		log.Println("this is a one man built agent, and that man is Matthew Baroi")

	default:
		log.Println("unknown command")
	}
}
