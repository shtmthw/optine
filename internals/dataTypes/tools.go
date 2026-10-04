package dataTypes

var WebSearch = NativeTypeTool{
	Type: "function",
	Function: NativeTypeToolFunction{
		Name: "web_search",
		Description: "Search the public internet using this tool. Use this when the user " +
			"asks about information that may have changed — current events, latest " +
			"software versions, current prices, recent releases, current people or " +
			"companies, or other time-sensitive facts. Do not use it for ordinary " +
			"knowledge that doesn't require current information.",
		Parameters: NativeTypeParameters{
			Type: "object",
			Properties: map[string]NativeTypeProperties{
				"query": {
					Type:        "string",
					Description: "The search query to run",
				},
			},
			Required: []string{"query"},
		},
	},
}

var ReadFile = NativeTypeTool{
	Type: "function",
	Function: NativeTypeToolFunction{
		Name:        "read_file",
		Description: "Read the contents of a file from the local filesystem. Use this tool when you need to inspect a file's contents.",
		Parameters: NativeTypeParameters{
			Type: "object",
			Properties: map[string]NativeTypeProperties{
				"path": {
					Type:        "string",
					Description: "The path to the file that should be read",
				},
			},
			Required: []string{"path"},
		},
	},
}

var Bash = NativeTypeTool{
	Type: "function",
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
			"To create or change the contents of a file, use the file write and edit tools. " +
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
