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

// MaxReadLines caps how many lines one read_file call returns.
const MaxReadLines = 2000

// ReadFileArgs parses the optional 1-based, inclusive line range. A missing or
// zero end_line means open ended. Values must be whole numbers.
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
		// 0 is the open-ended sentinel.
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

// ReadFile returns lines startLine..endLine (1-based, inclusive). A
// non-positive endLine means open ended. At most MaxReadLines lines are
// returned; the reply reports any clamp and the next start_line to request.
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

	// One probe tells us whether the capped window continues.
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

// readFooter builds the paging notice for a clamped or incomplete window.
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
