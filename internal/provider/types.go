package provider

import "encoding/json"

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message represents a single message in the conversation.
// For assistant messages: Content + optional ToolCalls.
// For tool result messages: ToolResult is set.
// Meta carries optional metadata (timestamps, token counts, model, etc.)
// that is not sent to the API but persisted in session files.
type Message struct {
	Role       Role              `json:"role"`
	Content    string            `json:"content,omitempty"`
	ToolCalls  []ToolCall        `json:"tool_calls,omitempty"`
	ToolResult *ToolResult       `json:"tool_result,omitempty"`
	Meta       map[string]string `json:"meta,omitempty"`
}

type ToolCall struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input string `json:"input"`
}

type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error,omitempty"`
}

type Request struct {
	SystemPrompt string
	Messages     []Message
	Tools        []ToolDef
	MaxTokens    int
}

type ToolDef struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

type Response struct {
	Text       string
	ToolCalls  []ToolCall
	Usage      Usage
	StopReason string // "end_turn", "tool_use", "max_tokens"
}

type Usage struct {
	InputTokens  int
	OutputTokens int
}

// StreamChunk is a tagged union for streaming responses.
// Check Kind to determine which field is populated.
type StreamChunk struct {
	Kind ChunkKind
	Text string     // when Kind == ChunkText
	Tool *ToolCall  // when Kind == ChunkToolUse
	Usage *Usage    // when Kind == ChunkUsage
	Err  error      // when Kind == ChunkError
}

type ChunkKind int

const (
	ChunkText    ChunkKind = iota
	ChunkToolUse
	ChunkUsage
	ChunkDone
	ChunkError
)
