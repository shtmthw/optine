package cli

import (
	"fmt"
	"os"
	"time"
)

var cliSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// withSpinner runs fn while showing a transient status line on stderr. Loading
// a model can take minutes with no output from Ollama, so this is the only
// progress signal. When stderr is not a terminal it prints one plain line
// instead of animation and escape codes.
func withSpinner(label string, fn func() error) error {
	if !stderrIsTerminal() {
		fmt.Fprintln(os.Stderr, label)
		return fn()
	}

	done := make(chan struct{})
	stopped := make(chan struct{})
	start := time.Now()

	go func() {
		defer close(stopped)

		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		for frame := 0; ; frame++ {
			select {
			case <-done:
				return
			case <-ticker.C:
				fmt.Fprintf(
					os.Stderr,
					"\r\033[K%s %s... %ds",
					cliSpinnerFrames[frame%len(cliSpinnerFrames)],
					label,
					int(time.Since(start).Seconds()),
				)
			}
		}
	}()

	err := fn()

	close(done)
	<-stopped
	fmt.Fprint(os.Stderr, "\r\033[K")

	return err
}

func stderrIsTerminal() bool {
	info, err := os.Stderr.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
