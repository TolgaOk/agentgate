package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/goleak"
)

func TestToAnthropicMsgText(t *testing.T) {
	m := Message{Role: RoleUser, Content: "hello"}
	am := toAnthropicMsg(m)
	if am.Role != "user" {
		t.Errorf("Role = %q, want user", am.Role)
	}
	var text string
	if err := json.Unmarshal(am.Content, &text); err != nil {
		t.Fatalf("Content is not a JSON string: %v", err)
	}
	if text != "hello" {
		t.Errorf("Content = %q, want hello", text)
	}
}

func TestToAnthropicMsgToolResult(t *testing.T) {
	m := Message{
		Role: RoleUser,
		ToolResult: &ToolResult{
			ToolCallID: "tc_123",
			Content:    "file contents here",
			IsError:    false,
		},
	}
	am := toAnthropicMsg(m)
	if am.Role != "user" {
		t.Errorf("Role = %q, want user", am.Role)
	}
	var blocks []anthropicContentBlock
	if err := json.Unmarshal(am.Content, &blocks); err != nil {
		t.Fatalf("Content is not a block array: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	if blocks[0].Type != "tool_result" {
		t.Errorf("Type = %q, want tool_result", blocks[0].Type)
	}
	if blocks[0].ToolUseID != "tc_123" {
		t.Errorf("ToolUseID = %q, want tc_123", blocks[0].ToolUseID)
	}
}

func TestToAnthropicMsgToolCalls(t *testing.T) {
	m := Message{
		Role:    RoleAssistant,
		Content: "Let me run that.",
		ToolCalls: []ToolCall{
			{ID: "tc_1", Name: "bash", Input: `{"command":"ls -la"}`},
		},
	}
	am := toAnthropicMsg(m)
	if am.Role != "assistant" {
		t.Errorf("Role = %q, want assistant", am.Role)
	}
	var blocks []anthropicContentBlock
	if err := json.Unmarshal(am.Content, &blocks); err != nil {
		t.Fatalf("Content is not a block array: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("len(blocks) = %d, want 2 (text + tool_use)", len(blocks))
	}
	if blocks[0].Type != "text" {
		t.Errorf("blocks[0].Type = %q, want text", blocks[0].Type)
	}
	if blocks[1].Type != "tool_use" {
		t.Errorf("blocks[1].Type = %q, want tool_use", blocks[1].Type)
	}
}

func TestParseAnthropicResponse(t *testing.T) {
	ar := anthropicResponse{
		Content: []anthropicContentBlock{
			{Type: "text", Text: "Here are the files:"},
			{Type: "tool_use", ID: "tc_1", Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)},
		},
		StopReason: "tool_use",
		Usage:      anthropicUsage{InputTokens: 100, OutputTokens: 50},
	}
	resp := parseAnthropicResponse(ar)
	if resp.Text != "Here are the files:" {
		t.Errorf("Text = %q", resp.Text)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Input != `{"command":"ls"}` {
		t.Errorf("ToolCalls[0].Input = %q, want raw JSON", resp.ToolCalls[0].Input)
	}
	if resp.Usage.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", resp.Usage.InputTokens)
	}
}

func TestChatStreamParsesSSE(t *testing.T) {
	defer goleak.VerifyNone(t)

	// Fake SSE server.
	sse := `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":42,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}

`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sse)
	}))
	defer server.Close()

	a := NewAnthropic("test-key", "test-model", 100)
	// Override the API URL by replacing the client transport.
	origAPI := anthropicAPI
	// We can't easily override the const, so let's test readStream directly.
	_ = origAPI
	_ = a

	// Test readStream directly with the SSE body.
	ctx := context.Background()
	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	ch := make(chan StreamChunk, 32)
	go a.readStream(ctx, resp.Body, ch)

	var texts []string
	var gotUsage bool
	var gotDone bool
	for chunk := range ch {
		switch chunk.Kind {
		case ChunkText:
			texts = append(texts, chunk.Text)
		case ChunkUsage:
			gotUsage = true
		case ChunkDone:
			gotDone = true
		case ChunkError:
			t.Fatalf("unexpected error: %v", chunk.Err)
		}
	}

	if got := join(texts); got != "Hello world" {
		t.Errorf("text = %q, want %q", got, "Hello world")
	}
	if !gotUsage {
		t.Error("expected usage chunk")
	}
	if !gotDone {
		t.Error("expected done chunk")
	}
}

func TestChatStreamToolUse(t *testing.T) {
	defer goleak.VerifyNone(t)

	sse := `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tc_1","name":"bash"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"comma"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"nd\":\"ls -la\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":3}}

event: message_stop
data: {"type":"message_stop"}

`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sse)
	}))
	defer server.Close()

	a := NewAnthropic("test-key", "test-model", 100)
	ctx := context.Background()
	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	ch := make(chan StreamChunk, 32)
	go a.readStream(ctx, resp.Body, ch)

	var tools []ToolCall
	for chunk := range ch {
		if chunk.Kind == ChunkToolUse {
			tools = append(tools, *chunk.Tool)
		}
	}

	if len(tools) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(tools))
	}
	if tools[0].Input != `{"command":"ls -la"}` {
		t.Errorf("Input = %q, want raw JSON", tools[0].Input)
	}
	if tools[0].Name != "bash" {
		t.Errorf("Name = %q, want bash", tools[0].Name)
	}
}

func TestParseAnthropicError(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{
			name:   "valid error",
			status: 401,
			body:   `{"type":"error","error":{"type":"authentication_error","message":"Invalid API key"}}`,
			want:   "anthropic: Invalid API key (HTTP 401)",
		},
		{
			name:   "rate limit",
			status: 429,
			body:   `{"type":"error","error":{"type":"rate_limit_error","message":"Rate limit exceeded"}}`,
			want:   "anthropic: Rate limit exceeded (HTTP 429)",
		},
		{
			name:   "unparseable body",
			status: 500,
			body:   `not json at all`,
			want:   "anthropic: HTTP 500: not json at all",
		},
		{
			name:   "empty message field",
			status: 400,
			body:   `{"type":"error","error":{"type":"bad_request"}}`,
			want:   `anthropic: HTTP 400: {"type":"error","error":{"type":"bad_request"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := parseAnthropicError(tt.status, []byte(tt.body))
			if err.Error() != tt.want {
				t.Errorf("got %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

func join(ss []string) string {
	result := ""
	for _, s := range ss {
		result += s
	}
	return result
}
