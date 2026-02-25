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

// ensureJSON returns s as-is if it's valid JSON, otherwise wraps it
// as {"command": "..."} for backwards compatibility with old session files
// where ToolCall.Input was a plain command string.
func ensureJSON(s string) string {
	if json.Valid([]byte(s)) {
		return s
	}
	b, _ := json.Marshal(map[string]string{"command": s})
	return string(b)
}
