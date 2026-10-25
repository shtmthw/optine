package harnessTools

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
)

// MaxReadLines caps how many lines a single read_file call returns, so one
// huge file cannot flood the model context or the TUI event feed. The cap is
// line based, not byte based: a caller that keeps a line under control is
// fine no matter how wide the file is.
const MaxReadLines = 2000

// ReadFileArgs reads the optional start_line and end_line arguments. Both are
// 1-based and inclusive. A missing start_line defaults to 1; a missing
// end_line means "open ended", i.e. read up to MaxReadLines lines from
// start_line. The returned end is 0 for the open-ended case.
//
// An explicit end_line of 0 is open ended too, byte for byte the same as
// omitting it: 0 is the sentinel, not a line number, so both spellings must
// mean the same thing. Any other value below start_line is a caller error.
//
// The values arrive as float64 from JSON decoding, so whole numbers only are
// accepted; anything else is a caller error, not a clamp.
func ReadFileArgs(arguments map[string]any) (start, end int, err error) {
	start = 1
	if raw, ok := arguments["start_line"]; ok {
		start, err = readIndexArg(raw, "start_line")
		if err != nil {
			return 0, 0, err
		}
		if start < 1 {
			return 0, 0, fmt.Errorf("read_file: start_line must be 1 or greater, got %d", start)
		}
	}
	if raw, ok := arguments["end_line"]; ok {
		end, err = readIndexArg(raw, "end_line")
		if err != nil {
			return 0, 0, err
		}
		// 0 is the open-ended sentinel, identical to omitting the key.
		// Only a positive end below start_line is a real error.
		if end > 0 && end < start {
			return 0, 0, fmt.Errorf("read_file: end_line %d is before start_line %d", end, start)
		}
		if end < 0 {
			return 0, 0, fmt.Errorf("read_file: end_line must be 0 (open ended) or 1 or greater, got %d", end)
		}
	}
	return start, end, nil
}

func readIndexArg(raw any, name string) (int, error) {
	switch v := raw.(type) {
	case float64:
		if v != math.Trunc(v) {
			return 0, fmt.Errorf("read_file: %s must be a whole number, got %v", name, v)
		}
		return int(v), nil
	case int:
		return v, nil
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return 0, fmt.Errorf("read_file: %s must be a whole number, got %q", name, v)
		}
		return int(n), nil
	default:
		return 0, fmt.Errorf("read_file: %s must be a number, got %T", name, raw)
	}
}

// ReadFile returns lines startLine..endLine (1-based, inclusive) of path. A
// non-positive endLine means open ended: read up to MaxReadLines lines from
// startLine.
//
// At most MaxReadLines lines are returned per call. When the requested range is
// longer than that, the window is clamped to MaxReadLines and the reply states
// how many of the requested lines were dropped. When the file continues past
// the returned window, the reply names the start_line to request next, so the
// model pages through a long file one capped window at a time.
//
// Reading is bounded: the file is never pulled fully into memory. Lines before
// startLine are skipped, at most MaxReadLines lines are collected, and a single
// one-line probe decides whether anything follows.
func ReadFile(path string, startLine, endLine int) (string, error) {
	if startLine < 1 {
		startLine = 1 // defensive; ReadFileArgs already enforces this
	}

	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("reading file %q: %w", path, err)
	}
	defer f.Close()

	reader := bufio.NewReader(f)

	// How many lines we are willing to return, and how many of the request
	// fall off the end of the cap.
	requested := MaxReadLines
	clamped := 0
	if endLine > 0 {
		if span := endLine - startLine + 1; span > MaxReadLines {
			clamped = span - MaxReadLines
			requested = MaxReadLines
		} else {
			requested = span
		}
	}

	// Skip the lines before the window. Hitting EOF here means the file is
	// shorter than startLine: say so instead of returning empty text.
	for line := 1; line < startLine; line++ {
		if _, err := reader.ReadString('\n'); err != nil {
			if err == io.EOF {
				return fmt.Sprintf("[read_file: %s has fewer than %d lines, nothing to read]\n", path, startLine), nil
			}
			return "", fmt.Errorf("reading file %q: %w", path, err)
		}
	}

	var b strings.Builder
	got := 0
	atEOF := false
	for got < requested {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			b.WriteString(line)
			got++
		}
		if err != nil {
			if err == io.EOF {
				atEOF = true
				break
			}
			return "", fmt.Errorf("reading file %q: %w", path, err)
		}
	}

	// If the window filled, one more read tells us whether the file goes on.
	// Nothing beyond that single line is read, so the cap still bounds the work.
	moreRemains := false
	if !atEOF && got == requested {
		rest, err := reader.ReadString('\n')
		switch {
		case len(rest) > 0 || err == nil:
			moreRemains = true
		case err != io.EOF:
			return "", fmt.Errorf("reading file %q: %w", path, err)
		}
	}

	return b.String() + readFooter(startLine, got, clamped, moreRemains), nil
}

// readFooter builds the paging notice for a window that stopped short of the
// end of the file, or for a request that asked for more lines than the cap
// allows. It is empty only when the whole remainder was returned and nothing
// was clamped, so an ordinary file reads exactly as before.
func readFooter(startLine, got, clamped int, moreRemains bool) string {
	if !moreRemains && clamped == 0 {
		return ""
	}
	last := startLine + got - 1
	note := fmt.Sprintf("showing lines %d-%d", startLine, last)
	if clamped > 0 {
		note += fmt.Sprintf("; the request was over the %d-line limit, %d line(s) truncated", MaxReadLines, clamped)
	}
	if moreRemains {
		note += fmt.Sprintf("; more lines follow, call read_file again with start_line=%d", last+1)
	} else {
		note += "; end of file reached"
	}
	return "\n[read_file: " + note + "]\n"
}
