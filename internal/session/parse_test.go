package session

import (
	"strings"
	"testing"

	"github.com/TolgaOk/agentgate/internal/provider"
)

func TestParse_WellFormed(t *testing.T) {
	input := `{"model":"anthropic/claude-sonnet-4-6"}
{"role":"user","content":"find all TODO comments","meta":{"date":"2026-02-21T14:32:00Z"}}
{"role":"assistant","content":"I found 14 TODO comments.","meta":{"model":"anthropic/claude-sonnet-4-6","input_tokens":"887","output_tokens":"42"}}
{"role":"user","content":"now fix the one in handler.go"}
{"role":"assistant","content":"Done. I've fixed the TODO in handler.go."}
`
	header, msgs, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if header.Model != "anthropic/claude-sonnet-4-6" {
		t.Errorf("Model = %q", header.Model)
	}
	if len(msgs) != 4 {
		t.Fatalf("got %d messages, want 4", len(msgs))
	}

	expects := []struct {
		role    provider.Role
		content string
	}{
		{provider.RoleUser, "find all TODO comments"},
		{provider.RoleAssistant, "I found 14 TODO comments."},
		{provider.RoleUser, "now fix the one in handler.go"},
		{provider.RoleAssistant, "Done. I've fixed the TODO in handler.go."},
	}
	for i, e := range expects {
		if msgs[i].Role != e.role || msgs[i].Content != e.content {
			t.Errorf("msgs[%d] = {%s, %q}, want {%s, %q}",
				i, msgs[i].Role, msgs[i].Content, e.role, e.content)
		}
	}

	if msgs[1].Meta["input_tokens"] != "887" {
		t.Errorf("msgs[1].Meta[input_tokens] = %q", msgs[1].Meta["input_tokens"])
	}
}

func TestParse_Empty(t *testing.T) {
	input := `{"model":"test-model"}
`
	header, msgs, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if header.Model != "test-model" {
		t.Errorf("Model = %q", header.Model)
	}
	if len(msgs) != 0 {
		t.Errorf("got %d messages, want 0", len(msgs))
	}
}

func TestParse_EmptyFile(t *testing.T) {
	_, msgs, err := Parse(strings.NewReader(""))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("got %d messages, want 0", len(msgs))
	}
}

func TestParse_ToolCallAndResult(t *testing.T) {
	input := `{"model":"test"}
{"role":"user","content":"list files"}
{"role":"assistant","content":"Let me check.","tool_calls":[{"id":"tc_1","name":"bash","input":"ls -la"}],"meta":{"input_tokens":"100"}}
{"role":"user","tool_result":{"tool_call_id":"tc_1","content":"file1.txt\nfile2.txt"},"meta":{"duration":"50ms"}}
{"role":"assistant","content":"Found 2 files."}
`
	_, msgs, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(msgs) != 4 {
		t.Fatalf("got %d messages, want 4", len(msgs))
	}

	// msg[1]: assistant with tool call
	if len(msgs[1].ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(msgs[1].ToolCalls))
	}
	tc := msgs[1].ToolCalls[0]
	if tc.ID != "tc_1" || tc.Name != "bash" || tc.Input != "ls -la" {
		t.Errorf("tool call = %+v", tc)
	}

	// msg[2]: tool result
	if msgs[2].ToolResult == nil {
		t.Fatal("msg[2].ToolResult is nil")
	}
	if msgs[2].ToolResult.ToolCallID != "tc_1" {
		t.Errorf("ToolCallID = %q", msgs[2].ToolResult.ToolCallID)
	}
	if msgs[2].ToolResult.Content != "file1.txt\nfile2.txt" {
		t.Errorf("Content = %q", msgs[2].ToolResult.Content)
	}
}

func TestParse_ToolError(t *testing.T) {
	input := `{"model":"test"}
{"role":"assistant","content":"Trying.","tool_calls":[{"id":"tc_1","name":"bash","input":"bad-cmd"}]}
{"role":"user","tool_result":{"tool_call_id":"tc_1","content":"not found","is_error":true}}
`
	_, msgs, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if !msgs[1].ToolResult.IsError {
		t.Error("expected IsError=true")
	}
}

func TestParse_BadHeader(t *testing.T) {
	_, _, err := Parse(strings.NewReader("not json"))
	if err == nil {
		t.Fatal("expected error for bad header")
	}
}
