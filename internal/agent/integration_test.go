package agent

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/TolgaOk/agentgate/internal/policy"
	"github.com/TolgaOk/agentgate/internal/provider"
)

// newTestProvider returns a provider for integration tests.
// Tries ANTHROPIC_API_KEY first, then OPENROUTER_API_KEY, skips if neither set.
func newTestProvider(t *testing.T, maxTokens int) (provider.Provider, string) {
	t.Helper()
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		return provider.NewAnthropic(key, "claude-sonnet-4-6", maxTokens), "claude-sonnet-4-6"
	}
	if key := os.Getenv("OPENROUTER_API_KEY"); key != "" {
		return provider.NewOpenRouter(key, "anthropic/claude-sonnet-4-6", maxTokens), "anthropic/claude-sonnet-4-6"
	}
	t.Skip("no API key set (ANTHROPIC_API_KEY or OPENROUTER_API_KEY)")
	return nil, ""
}

func TestIntegrationTextOnly(t *testing.T) {
	p, model := newTestProvider(t, 256)
	var buf bytes.Buffer

	a := &Agent{
		Provider:     p,
		Policy:       policy.Default(),
		Model:        model,
		SystemPrompt: "You are a helpful assistant. Be very brief.",
		MaxTokens:    256,
		MaxSteps:     10,
		Out:          &buf,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	text, usage, err := a.Run(ctx, "What is 2+2? Reply with just the number.")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Response: %q", text)
	t.Logf("Usage: in=%d out=%d", usage.InputTokens, usage.OutputTokens)
	t.Logf("Output: %s", buf.String())
}

func TestIntegrationToolCall(t *testing.T) {
	p, model := newTestProvider(t, 512)
	var buf bytes.Buffer

	a := &Agent{
		Provider:     p,
		Policy:       policy.Default(),
		Model:        model,
		SystemPrompt: "You have a bash tool. Use it to run commands. Be brief.",
		MaxTokens:    512,
		MaxSteps:     10,
		Out:          &buf,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	text, usage, err := a.Run(ctx, "Run 'echo hello_from_agent' and tell me the output.")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Response: %q", text)
	t.Logf("Usage: in=%d out=%d", usage.InputTokens, usage.OutputTokens)
	t.Logf("Full output:\n%s", buf.String())
}
