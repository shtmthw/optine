package harnessPermissions

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/mattthew/optine/internal/approval"
)

// Answer is the user's response to an approval prompt.
type Answer int

const (
	No       Answer = iota // zero value: anything unclear is a no
	Once                   // this call only
	Remember               // this tool for the rest of the session
)

// AskFunc prompts the user to approve one tool call.
func AskFunc(tool string, reader *bufio.Reader, arguments map[string]any, content string) (Answer, error) {
	log.Printf("\nThe agent wants to run:\n  tool: %s", tool)

	encodedArgs, err := json.MarshalIndent(arguments, "  ", "  ")
	if err != nil {
		return No, fmt.Errorf("encoding tool arguments for approval: %w", err)
	}

	log.Printf("  arguments:\n%s", encodedArgs)

	if strings.TrimSpace(content) != "" {
		log.Printf("  description: %s", content)
	}

	// Notify the active interface that a prompt is up; the CLI ignores it.
	approval.Notify()

	log.Print("Allow? [y] once  [a] always (this tool/command)  [N] no: ")

	line, err := reader.ReadString('\n')
	if err != nil {
		return No, err
	}

	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return Once, nil

	case "a", "always":
		return Remember, nil

	default:
		return No, nil
	}
}
