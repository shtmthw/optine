package systemPrompts

import "fmt"

// MaxReadLines is the line cap read_file enforces. It is duplicated here as a
// prompt fact because the system prompts teach paging and cannot import
// harnessTools (that would be a cycle: harnessTools pulls in the prompt-facing
// tooling). Keep this in sync with harnessTools.MaxReadLines; the classifier
// tests assert the same number on the tool side.
const MaxReadLines = 2000

// readFileSection renders the read_file paging teaching shared by
// NativeToolSystemPrompt and NonNativeToolSystemPrompt. The read_file schema in
// dataTypes/tools.go and harnessTools.ReadFile describe the same fact set;
// change one, check the others.
func readFileSection() string {
	return fmt.Sprintf(`read_file paging
read_file returns at most %d lines per call. Do not try to read a whole long file in one call.

To read a file longer than %d lines, page through it one window at a time, advancing start_line by %d each call and passing the same path:

  read_file(path, start_line=1,    end_line=%d)
  read_file(path, start_line=%d,   end_line=%d)
  read_file(path, start_line=%d,   end_line=%d)
  read_file(path, start_line=%d,   end_line=%d)

If a request asks for more than %d lines, only the first %d are returned and the reply tells you how many were truncated. Each reply that stops before the end of the file ends with a [read_file: ...] line naming the next start_line to request, for example start_line=%d. Follow that hint until a reply has no such line, which means you reached the end of the file.

start_line and end_line are both optional. Omit end_line, or pass end_line=0, and you read up to %d lines from start_line; the two spellings mean exactly the same thing. Never pass a negative end_line.

A single reply never contains more than %d lines, even when the range you ask for is wider.`,
		MaxReadLines, MaxReadLines, MaxReadLines,
		MaxReadLines,
		MaxReadLines+1, 2*MaxReadLines,
		2*MaxReadLines+1, 3*MaxReadLines,
		3*MaxReadLines+1, 4*MaxReadLines,
		MaxReadLines, MaxReadLines,
		MaxReadLines+1,
		MaxReadLines,
		MaxReadLines)
}
