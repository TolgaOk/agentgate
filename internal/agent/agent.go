package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	agexec "github.com/TolgaOk/agentgate/internal/exec"
	"github.com/TolgaOk/agentgate/internal/policy"
	"github.com/TolgaOk/agentgate/internal/provider"
)

const maxSteps = 20

// Exit codes for structured error reporting.
const (
	ExitOK      = 0
	ExitAgent   = 1 // agent loop error (max steps, stream error)
	ExitUsage   = 2 // bad config, missing args
	ExitTimeout = 3 // context deadline / timeout
)

// Status returns a machine-readable status string and exit code for the given error.
func Status(err error) (string, int) {
	if err == nil {
		return "ok", ExitOK
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout", ExitTimeout
	}
	return "error", ExitAgent
}

type Agent struct {
	Provider     provider.Provider
	ProviderName string
	Policy       policy.Policy
	Model        string
	SystemPrompt string
	MaxTokens    int
	SessionID    string
	Out          io.Writer
}

func (a *Agent) out() io.Writer {
	if a.Out != nil {
		return a.Out
	}
	return io.Discard
}

// Run executes a single agent turn: send message, stream response,
// execute tool calls, repeat until done or max steps reached.
func (a *Agent) Run(ctx context.Context, userMessage string) (string, provider.Usage, error) {
	messages := []provider.Message{
		{Role: provider.RoleUser, Content: userMessage},
	}
	text, usage, _, err := a.RunMessages(ctx, messages)
	return text, usage, err
}

// RunMessages executes the agent loop with the given message history.
// Returns the final text, accumulated usage, the full message history
// (including all intermediate tool calls and results with metadata), and any error.
func (a *Agent) RunMessages(ctx context.Context, messages []provider.Message) (string, provider.Usage, []provider.Message, error) {
	var totalUsage provider.Usage

	for step := range maxSteps {
		req := provider.Request{
			SystemPrompt: a.SystemPrompt,
			Messages:     messages,
			Tools:        []provider.ToolDef{provider.BashToolDef()},
			MaxTokens:    a.MaxTokens,
		}

		start := time.Now()
		stream, err := a.Provider.ChatStream(ctx, req)
		if err != nil {
			return "", totalUsage, messages, fmt.Errorf("agent: step %d: %w", step, err)
		}

		// Consume stream.
		var text strings.Builder
		var toolCalls []provider.ToolCall
		var stepUsage provider.Usage

		for chunk := range stream {
			switch chunk.Kind {
			case provider.ChunkText:
				text.WriteString(chunk.Text)
				fmt.Fprint(a.out(), chunk.Text)
			case provider.ChunkToolUse:
				toolCalls = append(toolCalls, *chunk.Tool)
			case provider.ChunkUsage:
				if chunk.Usage.InputTokens > 0 {
					stepUsage.InputTokens = chunk.Usage.InputTokens
				}
				if chunk.Usage.OutputTokens > 0 {
					stepUsage.OutputTokens = chunk.Usage.OutputTokens
				}
			case provider.ChunkError:
				return "", totalUsage, messages, fmt.Errorf("agent: step %d: stream: %w", step, chunk.Err)
			}
		}

		totalUsage.InputTokens += stepUsage.InputTokens
		totalUsage.OutputTokens += stepUsage.OutputTokens
		latency := time.Since(start)


		// Append assistant message with metadata.
		messages = append(messages, provider.Message{
			Role:      provider.RoleAssistant,
			Content:   text.String(),
			ToolCalls: toolCalls,
			Meta: map[string]string{
				"model":         a.Model,
				"date":          time.Now().UTC().Format(time.RFC3339),
				"duration":      latency.Round(time.Millisecond).String(),
				"input_tokens":  strconv.Itoa(stepUsage.InputTokens),
				"output_tokens": strconv.Itoa(stepUsage.OutputTokens),
			},
		})

		// No tool calls — we're done.
		if len(toolCalls) == 0 {
			fmt.Fprintln(a.out())
			return text.String(), totalUsage, messages, nil
		}

		// Execute each tool call.
		for _, tc := range toolCalls {
			toolStart := time.Now()
			result := a.executeTool(ctx, tc)
			toolDuration := time.Since(toolStart)
			messages = append(messages, provider.Message{
				Role:       provider.RoleUser,
				ToolResult: &result,
				Meta: map[string]string{
					"date":     time.Now().UTC().Format(time.RFC3339),
					"duration": toolDuration.Round(time.Millisecond).String(),
				},
			})
		}
	}

	return "", totalUsage, messages, fmt.Errorf("agent: exceeded max steps (%d)", maxSteps)
}

func (a *Agent) executeTool(ctx context.Context, tc provider.ToolCall) provider.ToolResult {
	decision := a.Policy.Check(tc.Input)

	switch decision.Kind {
	case policy.Block:
		fmt.Fprintf(a.out(), "\n[BLOCKED] %s: %s\n", tc.Input, decision.Reason)
		return provider.ToolResult{
			ToolCallID: tc.ID,
			Content:    fmt.Sprintf("BLOCKED: %s", decision.Reason),
			IsError:    true,
		}

	case policy.Confirm:
		if !confirmFromTTY(tc.Input) {
			fmt.Fprintln(a.out(), "\n[DENIED by user]")
			return provider.ToolResult{
				ToolCallID: tc.ID,
				Content:    "User denied execution",
				IsError:    true,
			}
		}
	}

	fmt.Fprintf(a.out(), "\n> %s\n", tc.Input)
	result, err := agexec.Execute(ctx, tc.Input, a.Policy.Timeout)
	if err != nil {
		return provider.ToolResult{
			ToolCallID: tc.ID,
			Content:    fmt.Sprintf("Error: %s", err),
			IsError:    true,
		}
	}
	return provider.ToolResult{
		ToolCallID: tc.ID,
		Content:    formatExecResult(result),
	}
}

func formatExecResult(r agexec.Result) string {
	var b strings.Builder
	if r.Stdout != "" {
		b.WriteString(r.Stdout)
	}
	if r.Stderr != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("STDERR: ")
		b.WriteString(r.Stderr)
	}
	if r.ExitCode != 0 {
		fmt.Fprintf(&b, "\n(exit code %d)", r.ExitCode)
	}
	return b.String()
}

// confirmFromTTY prompts the user on /dev/tty for confirmation.
func confirmFromTTY(command string) bool {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	defer tty.Close()

	fmt.Fprintf(tty, "Execute: %s\nConfirm? [y/N] ", command)
	var response string
	fmt.Fscanln(tty, &response)
	return strings.ToLower(strings.TrimSpace(response)) == "y"
}
