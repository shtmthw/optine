package harnessTools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// numbered returns a file body of n lines, each "line N\n".
func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func TestReadFileReturnsWholeSmallFile(t *testing.T) {
	path := writeTemp(t, "a.txt", "one\ntwo\nthree\n")

	got, err := ReadFile(path, 1, 0)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got != "one\ntwo\nthree\n" {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "read_file:") {
		t.Fatalf("a small file must not carry a paging note: %q", got)
	}
}

func TestReadFileWholeFileNotTruncated(t *testing.T) {
	path := writeTemp(t, "exact.txt", numbered(500))

	got, err := ReadFile(path, 1, 0)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(got, "truncated") || strings.Contains(got, "more lines follow") {
		t.Fatalf("a file under the cap must not be flagged: %q", got)
	}
	if !strings.Contains(got, "line 500") {
		t.Fatalf("expected the last line, got tail: %q", tail(got, 80))
	}
}

func TestReadFilePagesLongFile(t *testing.T) {
	path := writeTemp(t, "big.txt", numbered(4500))

	// Window 1: lines 1-2000, must paginate.
	got1, err := ReadFile(path, 1, 2000)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(got1, "line 2000") {
		t.Fatalf("window 1 should end at line 2000: %q", tail(got1, 80))
	}
	if strings.Contains(got1, "line 2001") {
		t.Fatalf("window 1 leaked into line 2001")
	}
	if !strings.Contains(got1, "call read_file again with start_line=2001") {
		t.Fatalf("window 1 should hint the next start_line: %q", tail(got1, 200))
	}

	// Window 3: lines 4001-4500, last window, no paging hint.
	got3, err := ReadFile(path, 4001, 6000)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(got3, "line 4500") {
		t.Fatalf("window 3 should reach the last line: %q", tail(got3, 80))
	}
	if strings.Contains(got3, "more lines follow") {
		t.Fatalf("the final window must not claim more lines follow: %q", tail(got3, 120))
	}
}

func TestReadFileOpenEndedWindow(t *testing.T) {
	path := writeTemp(t, "big.txt", numbered(3000))

	// end_line omitted (0): read up to the cap from start_line.
	got, err := ReadFile(path, 1, 0)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(got, "line 2000") || strings.Contains(got, "line 2001") {
		t.Fatalf("open-ended read should stop exactly at the cap: %q", tail(got, 80))
	}
	if !strings.Contains(got, "start_line=2001") {
		t.Fatalf("open-ended read should hint the next window: %q", tail(got, 200))
	}
}

func TestReadFileStartBeyondEOF(t *testing.T) {
	path := writeTemp(t, "a.txt", numbered(10))

	got, err := ReadFile(path, 100, 0)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(got, "fewer than 100 lines") {
		t.Fatalf("starting past EOF should say so: %q", got)
	}
}

func TestReadFileClampsOverLongRangeAndReportsTruncation(t *testing.T) {
	path := writeTemp(t, "big.txt", numbered(10))

	// Request 1-5000 (5000 lines) on a file with only 10: the request is
	// clamped to 2000 and reported, but the file ends first so no paging hint.
	got, err := ReadFile(path, 1, 5000)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(got, "truncated") {
		t.Fatalf("an over-long request must be reported as truncated: %q", tail(got, 200))
	}
	if !strings.Contains(got, "line 10") {
		t.Fatalf("the whole short file should still be returned: %q", tail(got, 80))
	}
}

func TestReadFileClampReportsCountOnLongFile(t *testing.T) {
	path := writeTemp(t, "big.txt", numbered(9000))

	// 1-9000 is 9000 lines; 7000 are clamped away.
	got, err := ReadFile(path, 1, 9000)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(got, "7000 line(s) truncated") {
		t.Fatalf("clamp count should be reported: %q", tail(got, 200))
	}
	if !strings.Contains(got, "start_line=2001") {
		t.Fatalf("a clamped long read should still hint paging: %q", tail(got, 250))
	}
}

func TestReadFileExactCapRangeNotTruncated(t *testing.T) {
	path := writeTemp(t, "big.txt", numbered(9000))

	// 5001-7000 is exactly 2000 lines: no clamp, but more follows.
	got, err := ReadFile(path, 5001, 7000)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(got, "truncated") {
		t.Fatalf("an exactly-2000-line range must not be called truncated: %q", tail(got, 200))
	}
	if !strings.Contains(got, "start_line=7001") {
		t.Fatalf("want next-window hint at 7001: %q", tail(got, 200))
	}
}

func TestReadFileArgsDefaultsAndValidation(t *testing.T) {
	start, end, err := ReadFileArgs(map[string]any{"path": "f"})
	if err != nil || start != 1 || end != 0 {
		t.Fatalf("defaults: got (%d, %d, %v)", start, end, err)
	}

	start, end, err = ReadFileArgs(map[string]any{"start_line": float64(11), "end_line": float64(20)})
	if err != nil || start != 11 || end != 20 {
		t.Fatalf("explicit: got (%d, %d, %v)", start, end, err)
	}

	bad := []map[string]any{
		{"start_line": float64(0)},                          // below 1
		{"start_line": float64(-3)},                         // negative
		{"start_line": float64(10), "end_line": float64(5)}, // end before start
		{"start_line": "5"},                                 // wrong type
		{"start_line": float64(1.5)},                        // not whole
		{"end_line": float64(-5)},                           // negative end
		{"start_line": float64(3), "end_line": float64(-1)}, // negative end
	}
	for _, args := range bad {
		if _, _, err := ReadFileArgs(args); err == nil {
			t.Fatalf("args %v should have been rejected", args)
		}
	}
}

// An explicit end_line of 0 must behave exactly like omitting it: both mean an
// open-ended read from start_line, and both return end=0.
func TestReadFileArgsEndLineZeroIsOpenEnded(t *testing.T) {
	omittedStart, omittedEnd, err := ReadFileArgs(map[string]any{"path": "f"})
	if err != nil {
		t.Fatalf("omitted end_line: %v", err)
	}
	zeroStart, zeroEnd, err := ReadFileArgs(map[string]any{"path": "f", "end_line": float64(0)})
	if err != nil {
		t.Fatalf("explicit end_line=0 must be accepted, got %v", err)
	}
	if omittedStart != zeroStart || omittedEnd != zeroEnd {
		t.Fatalf("omitted (%d,%d) and explicit 0 (%d,%d) must match", omittedStart, omittedEnd, zeroStart, zeroEnd)
	}
	if omittedStart != 1 || omittedEnd != 0 {
		t.Fatalf("both should be (1,0), got (%d,%d)", omittedStart, omittedEnd)
	}

	s, e, err := ReadFileArgs(map[string]any{"start_line": float64(10), "end_line": float64(0)})
	if err != nil || s != 10 || e != 0 {
		t.Fatalf("start=10 end_line=0: got (%d,%d,%v)", s, e, err)
	}
}

// The two spellings must also produce identical output from ReadFile.
func TestReadFileEndLineZeroMatchesOmitted(t *testing.T) {
	path := writeTemp(t, "big.txt", numbered(3000))

	omitted, err := ReadFile(path, 1, 0)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	zeroStart, zeroEnd, err := ReadFileArgs(map[string]any{"path": "big.txt", "end_line": float64(0)})
	if err != nil {
		t.Fatalf("ReadFileArgs: %v", err)
	}
	explicit, err := ReadFile(path, zeroStart, zeroEnd)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if omitted != explicit {
		t.Fatalf("explicit 0 and omitted must read identically")
	}
}

func TestReadFileMissingErrors(t *testing.T) {
	if _, err := ReadFile(filepath.Join(t.TempDir(), "nope.txt"), 1, 0); err == nil {
		t.Fatal("reading a missing file should error")
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
