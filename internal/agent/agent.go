package agent

import (
	"context"
	"encoding/json"
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
	"github.com/TolgaOk/agentgate/internal/skill"
)


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
	MaxSteps     int
	SessionID    string
	Skills       []skill.Skill
	NoTool       bool
	AutoAccept   bool
	Out          io.Writer
	OnStep       func(newMsgs []provider.Message) // called after each step with new messages
}

func (a *Agent) flushStep(msgs []provider.Message) {
	if a.OnStep != nil {
		a.OnStep(msgs)
	}
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

	for step := range a.MaxSteps {
		var tools []provider.ToolDef
		if !a.NoTool {
			for _, s := range a.Skills {
				if s.IsTool() {
					tools = append(tools, s.ToToolDef())
				}
			}
			if a.hasReadableSkills() {
				tools = append(tools, readSkillToolDef())
			}
		}
		req := provider.Request{
			SystemPrompt: a.SystemPrompt,
			Messages:     messages,
			Tools:        tools,
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

		if ctx.Err() != nil {
			return "", totalUsage, messages, fmt.Errorf("agent: step %d: %w", step, ctx.Err())
		}

		totalUsage.InputTokens += stepUsage.InputTokens
		totalUsage.OutputTokens += stepUsage.OutputTokens
		latency := time.Since(start)


		// Append assistant message with metadata.
		stepStart := len(messages)
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
			a.flushStep(messages[stepStart:])
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
		a.flushStep(messages[stepStart:])
	}

	return "", totalUsage, messages, fmt.Errorf("agent: exceeded max steps (%d)", a.MaxSteps)
}

func (a *Agent) executeTool(ctx context.Context, tc provider.ToolCall) provider.ToolResult {
	if tc.Name == "read_skill" {
		return a.handleReadSkill(tc)
	}

	s := a.findSkill(tc.Name)
	if s == nil {
		return provider.ToolResult{
			ToolCallID: tc.ID,
			Content:    fmt.Sprintf("unknown tool %q", tc.Name),
			IsError:    true,
		}
	}

	decision := a.Policy.Check(s.Tool.Command)
	if decision.Kind == policy.Block {
		fmt.Fprintf(a.out(), "\n[BLOCKED] %s: %s\n", tc.Name, decision.Reason)
		return provider.ToolResult{
			ToolCallID: tc.ID,
			Content:    fmt.Sprintf("BLOCKED: %s", decision.Reason),
			IsError:    true,
		}
	}

	argv, err := s.BuildArgv(tc.Input)
	if err != nil {
		return provider.ToolResult{
			ToolCallID: tc.ID,
			Content:    fmt.Sprintf("Error: %s", err),
			IsError:    true,
		}
	}

	display := strings.Join(argv, " ")

	switch decision.Kind {
	case policy.Confirm:
		if !a.AutoAccept && !confirmFromTTY(display) {
			fmt.Fprintln(a.out(), "\n[DENIED by user]")
			return provider.ToolResult{
				ToolCallID: tc.ID,
				Content:    "User denied execution",
				IsError:    true,
			}
		}
	}

	fmt.Fprintf(a.out(), "\n> %s\n", display)
	result, err := agexec.ExecuteDirect(ctx, argv, a.Policy.Timeout)
	if err != nil {
		return provider.ToolResult{
			ToolCallID: tc.ID,
			Content:    fmt.Sprintf("Error: %s", err),
			IsError:    true,
		}
	}
	if result.Stdout != "" {
		fmt.Fprintf(a.out(), "\033[90m%s\033[0m\n", strings.TrimSpace(result.Stdout))
	}
	return provider.ToolResult{
		ToolCallID: tc.ID,
		Content:    formatExecResult(result),
	}
}

func (a *Agent) findSkill(name string) *skill.Skill {
	for i := range a.Skills {
		if a.Skills[i].Name == name {
			return &a.Skills[i]
		}
	}
	return nil
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

func readSkillToolDef() provider.ToolDef {
	return provider.ToolDef{
		Name:        "read_skill",
		Description: "Read the full context of a skill by name",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","description":"skill name"}},"required":["name"]}`),
	}
}

func (a *Agent) hasReadableSkills() bool {
	for _, s := range a.Skills {
		if !s.IsTool() && s.Body != "" {
			return true
		}
	}
	return false
}

func (a *Agent) handleReadSkill(tc provider.ToolCall) provider.ToolResult {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(tc.Input), &params); err != nil {
		return provider.ToolResult{ToolCallID: tc.ID, Content: "invalid input: expected {\"name\": \"...\"}", IsError: true}
	}
	for _, s := range a.Skills {
		if s.Name == params.Name {
			if s.Body == "" {
				return provider.ToolResult{ToolCallID: tc.ID, Content: "skill has no readable content", IsError: true}
			}
			fmt.Fprintf(a.out(), "\n> read_skill %s\n", params.Name)
			return provider.ToolResult{ToolCallID: tc.ID, Content: s.Body}
		}
	}
	return provider.ToolResult{ToolCallID: tc.ID, Content: fmt.Sprintf("skill %q not found", params.Name), IsError: true}
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
