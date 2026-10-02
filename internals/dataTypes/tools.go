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
