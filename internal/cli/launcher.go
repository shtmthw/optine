package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/mattthew/optine/internal/slashCommands"
	"github.com/mattthew/optine/internal/tui"
)

// errCancelled means the user backed out of a picker or prompt. The widget
// already shows that it was cancelled, so callers print nothing more.
var errCancelled = errors.New("cancelled")

// Start runs the launcher until the user quits: it shows the banner, then
// reads and runs launcher commands. Starting a session hands the terminal to
// the agent interface; leaving it with ctrl+c comes back here, and /quit
// anywhere exits.
func Start(reader *bufio.Reader) {
	log.SetFlags(0)

	commands := slashCommands.For(slashCommands.Launcher)
	fmt.Print(tui.Banner(terminalWidth(), commands))
	fmt.Println()

	for {
		line, ok := readLauncherCommand(reader, commands)
		if !ok {
			return
		}

		if quit := dispatch(line, false, reader); quit {
			return
		}
	}
}

// readLauncherCommand returns the next command line. ok is false when the
// user asked to leave (ctrl+c, ctrl+d, EOF).
func readLauncherCommand(reader *bufio.Reader, commands []slashCommands.Command) (string, bool) {
	if interactive() {
		line, ok, err := tui.PromptCommand(commands)
		if err == nil {
			return line, ok
		}

		launcherOut.warn("interactive prompt unavailable (%v), using plain input", err)
		interactive = func() bool { return false }
	}

	fmt.Fprint(os.Stderr, "> ")

	input, err := reader.ReadString('\n')
	if err != nil {
		if err != io.EOF {
			launcherOut.fail("reading input: %v", err)
		}
		return "", false
	}

	return input, true
}

// option is one choice in a launcher picker: value is what the code acts
// on, label is what the user sees.
type option struct {
	value string
	label string
}

// plainOptions turns values into options labelled with themselves.
func plainOptions(values []string) []option {
	options := make([]option, len(values))
	for i, v := range values {
		options[i] = option{value: v, label: v}
	}

	return options
}

// chooseOption asks the user to pick one option: an arrow-key list in a
// terminal, a numbered list otherwise. It returns the option's value, or
// errCancelled when the user backed out.
func chooseOption(reader *bufio.Reader, title string, options []option) (string, error) {
	if len(options) == 0 {
		return "", errors.New("nothing to choose from")
	}

	labels := make([]string, len(options))
	for i, o := range options {
		labels[i] = o.label
	}

	if interactive() {
		choice, ok, err := tui.Select(title, labels)
		if err == nil {
			if !ok {
				return "", errCancelled
			}
			for _, o := range options {
				if o.label == choice {
					return o.value, nil
				}
			}
			return "", fmt.Errorf("unknown choice %q", choice)
		}

		launcherOut.warn("interactive list unavailable (%v), using a numbered list", err)
	}

	fmt.Fprintln(os.Stderr, title)
	for i, label := range labels {
		fmt.Fprintf(os.Stderr, "  %d. %s\n", i+1, label)
	}
	fmt.Fprint(os.Stderr, "number: ")

	label, err := selectOption(reader, 5, labels)
	if err != nil {
		return "", err
	}

	for _, o := range options {
		if o.label == label {
			return o.value, nil
		}
	}

	return "", fmt.Errorf("unknown choice %q", label)
}

// readAPIKey asks for an API key, hiding it in a terminal.
func readAPIKey(reader *bufio.Reader, name string) (string, error) {
	if interactive() {
		key, ok, err := tui.PromptSecret(name + " API key")
		if err == nil {
			if !ok {
				return "", errCancelled
			}
			return key, nil
		}

		launcherOut.warn("hidden input unavailable (%v), the key will be visible as you type", err)
	}

	fmt.Fprintf(os.Stderr, "Paste the %s API key and press enter: ", name)

	key, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(key), nil
}
