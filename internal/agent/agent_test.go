package agent

import (
	"bytes"
	"context"
	"testing"

	"go.uber.org/goleak"

	"github.com/TolgaOk/agentgate/internal/policy"
	"github.com/TolgaOk/agentgate/internal/provider"
	"github.com/TolgaOk/agentgate/internal/skill"
)

// mockProvider sends canned responses based on call count.
type mockProvider struct {
	responses []mockResponse
	callCount int
}

type mockResponse struct {
	text      string
	toolCalls []provider.ToolCall
	usage     provider.Usage
}

func (m *mockProvider) Chat(_ context.Context, _ provider.Request) (provider.Response, error) {
	panic("not used in agent tests")
}

func (m *mockProvider) ChatStream(_ context.Context, _ provider.Request) (<-chan provider.StreamChunk, error) {
	idx := m.callCount
	if idx >= len(m.responses) {
		idx = len(m.responses) - 1
	}
	m.callCount++
	r := m.responses[idx]

	ch := make(chan provider.StreamChunk, 16)
	go func() {
		defer close(ch)
		if r.text != "" {
			ch <- provider.StreamChunk{Kind: provider.ChunkText, Text: r.text}
		}
		for i := range r.toolCalls {
			ch <- provider.StreamChunk{Kind: provider.ChunkToolUse, Tool: &r.toolCalls[i]}
		}
		ch <- provider.StreamChunk{Kind: provider.ChunkUsage, Usage: &r.usage}
		ch <- provider.StreamChunk{Kind: provider.ChunkDone}
	}()
	return ch, nil
}

func TestTextOnlyResponse(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock := &mockProvider{
		responses: []mockResponse{
			{text: "The answer is 4.", usage: provider.Usage{InputTokens: 10, OutputTokens: 5}},
		},
	}

	var buf bytes.Buffer
	a := &Agent{
		Provider: mock,
		Policy:   policy.Default(),
		MaxSteps: 10,
		Out:      &buf,
	}

	text, usage, err := a.Run(context.Background(), "what is 2+2")
	if err != nil {
		t.Fatal(err)
	}
	if text != "The answer is 4." {
		t.Errorf("text = %q", text)
	}
	if usage.InputTokens != 10 {
		t.Errorf("InputTokens = %d, want 10", usage.InputTokens)
	}
	if mock.callCount != 1 {
		t.Errorf("callCount = %d, want 1", mock.callCount)
	}
}

func TestToolCallThenText(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock := &mockProvider{
		responses: []mockResponse{
			// First call: model requests a tool call.
			{
				text: "Let me check.",
				toolCalls: []provider.ToolCall{
					{ID: "tc_1", Name: "bash", Input: "echo hello"},
				},
				usage: provider.Usage{InputTokens: 20, OutputTokens: 10},
			},
			// Second call: model returns text.
			{
				text:  "The output was hello.",
				usage: provider.Usage{InputTokens: 30, OutputTokens: 15},
			},
		},
	}

	var buf bytes.Buffer
	a := &Agent{
		Provider: mock,
		Policy:   policy.Default(),
		MaxSteps: 10,
		Out:      &buf,
	}

	text, usage, err := a.Run(context.Background(), "run echo hello")
	if err != nil {
		t.Fatal(err)
	}
	if text != "The output was hello." {
		t.Errorf("text = %q", text)
	}
	if mock.callCount != 2 {
		t.Errorf("callCount = %d, want 2", mock.callCount)
	}
	if usage.InputTokens != 50 {
		t.Errorf("InputTokens = %d, want 50", usage.InputTokens)
	}
}

func TestBlockedCommand(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock := &mockProvider{
		responses: []mockResponse{
			{
				toolCalls: []provider.ToolCall{
					{ID: "tc_1", Name: "bash", Input: `{"command":"sudo whoami"}`},
				},
				usage: provider.Usage{InputTokens: 10, OutputTokens: 5},
			},
			// After blocked result, model gives up.
			{text: "I can't do that.", usage: provider.Usage{InputTokens: 20, OutputTokens: 10}},
		},
	}

	var buf bytes.Buffer
	a := &Agent{
		Provider: mock,
		Policy: policy.Policy{
			Blocked: []string{"bash"},
		},
		MaxSteps: 10,
		Skills: []skill.Skill{{
			Name: "bash",
			Tool: &skill.ToolMeta{Command: "bash"},
		}},
		Out: &buf,
	}

	text, _, err := a.Run(context.Background(), "delete everything")
	if err != nil {
		t.Fatal(err)
	}
	if text != "I can't do that." {
		t.Errorf("text = %q", text)
	}
	// Verify the BLOCKED message was written.
	if !bytes.Contains(buf.Bytes(), []byte("BLOCKED")) {
		t.Error("expected BLOCKED in output")
	}
}

func TestMaxStepsExceeded(t *testing.T) {
	defer goleak.VerifyNone(t)

	// Always returns a tool call, never finishes.
	mock := &mockProvider{
		responses: []mockResponse{
			{
				toolCalls: []provider.ToolCall{
					{ID: "tc_1", Name: "bash", Input: "echo loop"},
				},
				usage: provider.Usage{InputTokens: 5, OutputTokens: 3},
			},
		},
	}

	var buf bytes.Buffer
	a := &Agent{
		Provider: mock,
		Policy:   policy.Default(),
		MaxSteps: 5,
		Out:      &buf,
	}

	_, _, err := a.Run(context.Background(), "loop forever")
	if err == nil {
		t.Error("expected error for max steps exceeded")
	}
	if mock.callCount != a.MaxSteps {
		t.Errorf("callCount = %d, want %d", mock.callCount, a.MaxSteps)
	}
}
