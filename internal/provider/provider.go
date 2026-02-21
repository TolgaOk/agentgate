package provider

import (
	"context"
	"encoding/json"
)

// Provider is the interface that all LLM backends implement.
type Provider interface {
	Chat(ctx context.Context, req Request) (Response, error)
	ChatStream(ctx context.Context, req Request) (<-chan StreamChunk, error)
}

// BashToolDef returns the tool definition for the bash tool.
func BashToolDef() ToolDef {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "The shell command to execute"
			}
		},
		"required": ["command"]
	}`)

	return ToolDef{
		Name:        "bash",
		Description: "Run a shell command and return output",
		InputSchema: schema,
	}
}
