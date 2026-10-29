package dataTypes

// WebSearch must stay in sync with crawlerSection and CrawlersAvailable.
var WebSearch = NativeTypeTool{
	Type: "function",
	Function: NativeTypeToolFunction{
		Name: "web_search",
		Description: "Search the public internet using this tool. Use this when the user " +
			"asks about information that may have changed — current events, latest " +
			"software versions, current prices, recent releases, current people or " +
			"companies, or other time-sensitive facts. Do not use it for ordinary " +
			"knowledge that doesn't require current information. " +
			"Pass the crawler argument to pick the backend: tavily (hosted, best " +
			"ranked quality, sends the query to a third party, API key stored in " +
			"auth.json) or searxng (the user's own instance, private, no quota). " +
			"The system prompt lists which crawlers are available this session.",
		Parameters: NativeTypeParameters{
			Type: "object",
			Properties: map[string]NativeTypeProperties{
				"query": {
					Type:        "string",
					Description: "The search query to run",
				},
				"crawler": {
					Type: "string",
					Description: "Required, no default. \"tavily\" runs a hosted search " +
						"(best quality, third party, API key from auth.json); \"searxng\" " +
						"runs the user's own instance (private, no quota). Pick searxng for " +
						"private queries and whenever tavily reports quota or key problems.",
				},
			},
			Required: []string{"query", "crawler"},
		},
	},
}

var ReadFile = NativeTypeTool{
	Type: "function",
	Function: NativeTypeToolFunction{
		Name: "read_file",
		Description: "Read lines from a file on the local filesystem. Use it to inspect a file's contents. " +
			"Returns at most 2000 lines per call. For a file longer than that, page through it with " +
			"start_line/end_line: read_file(path, start_line=1, end_line=2000), then " +
			"start_line=2001, end_line=4000, and so on. The reply names the next start_line to request " +
			"and states how many of the requested lines were truncated when the range is over the limit.",
		Parameters: NativeTypeParameters{
			Type: "object",
			Properties: map[string]NativeTypeProperties{
				"path": {
					Type:        "string",
					Description: "The path to the file that should be read",
				},
				"start_line": {
					Type:        "integer",
					Description: "Optional, 1-based first line to return. Defaults to 1.",
				},
				"end_line": {
					Type: "integer",
					Description: "Optional, 1-based last line to return (inclusive). Omit it, or pass 0, " +
						"for an open range capped at 2000 lines; both mean the same thing. A range " +
						"longer than 2000 lines is clamped to 2000.",
				},
			},
			Required: []string{"path"},
		},
	},
}

var Bash = NativeTypeTool{Type: "function",
	Function: NativeTypeToolFunction{
		Name: "bash",
		Description: "Run a shell command in the user's workspace and return its output. " +
			"Use it for listing, searching, building, testing and simple file operations " +
			"(ls, find, grep, mkdir, cp, mv, rm, go test). " +
			"Keep commands simple: plain commands joined with &&, ||, ; or |, with redirects " +
			"like > and 2>&1. Every command is checked before it runs, and is rejected if it " +
			"uses $(...), backticks, variables, loops, if statements, subshells, heredocs, " +
			"background jobs (&) or brace expansion like {a,b}. " +
			"Every call starts in the workspace root, and cd does not carry over to the next call. " +
			"To create or change the contents of a file, use simple commands with redirects " +
			"(echo ... > file, cat >> file). " +
			"Some commands need the user's approval. If a command is denied, do not retry it unchanged.",
		Parameters: NativeTypeParameters{
			Type: "object",
			Properties: map[string]NativeTypeProperties{
				"command": {
					Type: "string",
					Description: "The shell command to run. Plain commands only: no $(...), backticks, " +
						"variables, loops, subshells, heredocs or background jobs.",
				},
				"description": {
					Type:        "string",
					Description: "One short sentence saying what the command does. Shown to the user when approval is needed.",
				},
				"timeout_ms": {
					Type:        "integer",
					Description: "Optional timeout in milliseconds. Default 30000, maximum 120000.",
				},
			},
			Required: []string{"command"},
		},
	},
}

var EditFile = NativeTypeTool{
	Type: "function",
	Function: NativeTypeToolFunction{
		Name: "edit_file",
		Description: "Replace one exact text block in an existing file with new text. " +
			"Use it after read_file when you need to change a file. " +
			"old_string must match exactly once; if it matches zero or multiple times the call fails. " +
			"Add more surrounding context to make it unique. The file must already exist. " +
			"Pass append=true to append new_string byte-for-byte to the end of the file instead (old_string is then ignored). " +
			"Texts over 256KB combined are rejected; split into smaller edits.",
		Parameters: NativeTypeParameters{
			Type: "object",
			Properties: map[string]NativeTypeProperties{
				"path": {
					Type:        "string",
					Description: "The path to the existing file to edit",
				},
				"old_string": {
					Type:        "string",
					Description: "The exact text to replace, must occur exactly once in the file (ignored when append is true)",
				},
				"new_string": {
					Type:        "string",
					Description: "The replacement text, or the text to append when append is true",
				},
				"append": {
					Type:        "boolean",
					Description: "Optional, defaults to false. When true, new_string is appended to the end of the file and old_string is ignored",
				},
			},
			Required: []string{"path", "old_string", "new_string"},
		},
	},
}

var WriteFile = NativeTypeTool{
	Type: "function",
	Function: NativeTypeToolFunction{
		Name: "write_file",
		Description: "Create a new file with the given content. " +
			"Use it when you need to create a file that does not exist yet. " +
			"Fails if the file already exists (use edit_file to change it). " +
			"Missing parent directories are created. Never writes through symlinks. " +
			"Content over 256KB is rejected; split into smaller writes.",
		Parameters: NativeTypeParameters{
			Type: "object",
			Properties: map[string]NativeTypeProperties{
				"path": {
					Type:        "string",
					Description: "The path of the file to create",
				},
				"content": {
					Type:        "string",
					Description: "The full content to write into the new file (may be empty)",
				},
			},
			Required: []string{"path", "content"},
		},
	},
}
