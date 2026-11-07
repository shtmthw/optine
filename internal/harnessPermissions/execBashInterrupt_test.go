package harnessPermissions

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Cancelling the parent context (a user interrupt) kills a running command
// promptly instead of waiting out its timeout, and says why in the output.
func TestExecBashContextInterruptKillsCommand(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)

	start := time.Now()
	text, code := ExecBashContext(ctx, "echo started; sleep 30", t.TempDir(), time.Minute)
	elapsed := time.Since(start)

	if elapsed > 10*time.Second {
		t.Fatalf("command ran %s after the interrupt", elapsed)
	}
	if code == 0 {
		t.Fatalf("killed command reported exit 0: %q", text)
	}
	if !strings.Contains(text, "started") {
		t.Fatalf("output before the interrupt lost: %q", text)
	}
	if !strings.Contains(text, "[interrupted by user, command killed]") {
		t.Fatalf("interrupt not reported: %q", text)
	}
	if strings.Contains(text, "timed out") {
		t.Fatalf("interrupt misreported as a timeout: %q", text)
	}
}
