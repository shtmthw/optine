package cli

import (
	"bufio"
	"log"
	"strings"
)

// taking in the case for /local
func RunCommand(reader *bufio.Reader) {
	// reader := bufio.NewReader(os.Stdin)

	command, err := reader.ReadString('\n')
	if err != nil {
		return
	}

	command = strings.TrimSpace(command)

	if !strings.HasPrefix(command, "/") {
		log.Println("Not a command that optine supports")
		return
	}

	switch command {
	case "/local":
		log.Println("starting /local execution")
		// the provider selection
		selectProvider(reader)

	default:
		log.Println("unknown command")
	}
}
