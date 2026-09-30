package cli

import (
	"bufio"
	"log"
	"strings"

	"github.com/mattthew/optine/internals/dataTypes"
	"github.com/mattthew/optine/internals/harnessCore"
)

func agentInterface(reader *bufio.Reader, agentConfig dataTypes.AgentConfig) (string, error) {

	for {
		userInput, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}

		userInput = strings.TrimSpace(userInput)

		if userInput == "" {
			continue
		}

		if strings.HasPrefix(userInput, "/") {
			RunCommand(userInput, true, reader)
			continue
		}

		result, err := harnessCore.AgentLoop(userInput, agentConfig, reader)
		if err != nil {
			return "", err
		}

		log.Println(result)

		continue
	}
}
