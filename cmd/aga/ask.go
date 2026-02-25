package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/TolgaOk/agentgate/internal/agent"
	"github.com/TolgaOk/agentgate/internal/prompt"
	"github.com/TolgaOk/agentgate/internal/provider"
	"github.com/TolgaOk/agentgate/internal/queue"
	"github.com/TolgaOk/agentgate/internal/render"
	"github.com/TolgaOk/agentgate/internal/session"
	"github.com/TolgaOk/agentgate/internal/skill"
)

func newAskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ask [prompt]",
		Short: "Run a prompt (conversation saved to context file)",
		Args:  cobra.ArbitraryArgs,
		RunE:  runAsk,
	}
	cmd.Flags().Bool("json", false, "JSON output to stdout")
	cmd.Flags().String("context", "", "Path to conversation context file (JSONL); created if missing")
	cmd.Flags().String("model", "", "Override model name")
	cmd.Flags().String("system-prompt", "", "Path to system prompt file (.md)")
	cmd.Flags().String("skill", "", "Path to directory of .md skill files to append to system prompt")
	cmd.Flags().Int("max-tokens", 0, "Override max output tokens")
	cmd.Flags().Duration("timeout", 0, "Overall timeout for the entire operation (e.g. 30s, 2m)")
	cmd.Flags().String("session-id", "", "Tag this call for metrics tracking")
	cmd.Flags().String("render", "", "Render markdown: auto, dark, light, raw")
	cmd.Flags().Bool("no-tool", false, "Disable tool usage (text-only mode)")
	cmd.Flags().Bool("auto-accept", false, "Auto-accept tool calls that require confirmation (blocked commands are still rejected)")
	return cmd
}

func runAsk(cmd *cobra.Command, args []string) error {
	// Build prompt from args or stdin.
	// Only read stdin when no args were provided (avoids blocking on piped stdin with empty args).
	userPrompt := strings.Join(args, " ")
	if len(args) == 0 && stdinPiped() {
		data, err := io.ReadAll(os.Stdin)
		if err == nil {
			userPrompt = strings.TrimSpace(string(data))
		}
	}
	if strings.TrimSpace(userPrompt) == "" {
		return fmt.Errorf("usage: aga ask [flags] \"prompt\"")
	}

	jsonMode, _ := cmd.Flags().GetBool("json")
	contextFile, _ := cmd.Flags().GetString("context")
	model, _ := cmd.Flags().GetString("model")
	systemPromptFile, _ := cmd.Flags().GetString("system-prompt")
	skillDir, _ := cmd.Flags().GetString("skill")
	maxTokensFlag, _ := cmd.Flags().GetInt("max-tokens")
	timeoutFlag, _ := cmd.Flags().GetDuration("timeout")
	sessionID, _ := cmd.Flags().GetString("session-id")
	renderFlag, _ := cmd.Flags().GetString("render")
	noTool, _ := cmd.Flags().GetBool("no-tool")
	autoAccept, _ := cmd.Flags().GetBool("auto-accept")

	e, err := setupEnv()
	if err != nil {
		fatal(err)
	}
	defer e.store.Close()

	// Apply per-invocation overrides.
	if model != "" {
		e.cfg.Model = model
		p, err := newProvider(e.cfg)
		if err != nil {
			fatal(err)
		}
		e.provider = queue.New(p, queue.Config{
			GlobalLimit:   e.cfg.ConcurrentGlobalLimit,
			ProviderLimit: e.cfg.ConcurrentPerProviderLimit,
			ProviderName:  e.cfg.Provider,
			Model:         e.cfg.Model,
		}, e.store)
	}

	// Load system prompt.
	var sysPrompt string
	if systemPromptFile != "" {
		data, err := os.ReadFile(systemPromptFile)
		if err != nil {
			fatal(err)
		}
		sysPrompt = string(data)
	} else {
		sysPrompt, err = prompt.Load()
		if err != nil {
			fatal(err)
		}
	}

	// Load skills from --skill directory.
	var skills []skill.Skill
	if skillDir != "" {
		skills, err = skill.ParseDir(skillDir)
		if err != nil {
			fatal(err)
		}
		for _, s := range skills {
			if s.Body != "" {
				sysPrompt = sysPrompt + "\n\n" + s.Body
			}
		}
	}

	maxTokens := e.cfg.MaxTokens
	if maxTokensFlag > 0 {
		maxTokens = maxTokensFlag
	}

	if timeoutFlag > 0 {
		e.pol.Timeout = timeoutFlag // also applies to tool execution
	}

	// Open or create context file.
	var sess *session.Session
	var contextStatus string
	modelTag := e.cfg.Provider + "/" + e.cfg.Model
	if contextFile != "" {
		if _, statErr := os.Stat(contextFile); statErr == nil {
			sess, err = session.Open(contextFile)
			if err != nil {
				fatal(err)
			}
			contextStatus = "context: " + sess.FilePath
		} else {
			sess, err = session.NewAt(contextFile, modelTag)
			if err != nil {
				fatal(err)
			}
			contextStatus = "context created: " + sess.FilePath
		}
	} else {
		ctxDir := filepath.Join(os.TempDir(), "agentgate")
		sess, err = session.New(ctxDir, modelTag)
		if err != nil {
			fatal(err)
		}
		contextStatus = "context: " + sess.FilePath
	}
	defer sess.Close()

	if !jsonMode {
		fmt.Fprintln(os.Stderr, styleDim.Render(contextStatus))
	}

	if sessionID == "" {
		sessionID = sess.ID
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if timeoutFlag > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeoutFlag)
		defer cancel()
	}

	// Append user prompt to context.
	userMsg := provider.Message{
		Role:    provider.RoleUser,
		Content: userPrompt,
		Meta:    map[string]string{"date": time.Now().UTC().Format(time.RFC3339)},
	}
	if err := sess.AppendMessage(userMsg); err != nil {
		fatal(err)
	}

	// Choose output writer.
	var out io.Writer
	var sr *render.StreamRenderer
	if jsonMode {
		out = io.Discard
	} else {
		renderStyle := parseRenderStyle(renderFlag)
		out, sr = makeOutput(renderStyle)
	}

	a := &agent.Agent{
		Provider:     e.provider,
		ProviderName: e.cfg.Provider,
		Policy:       e.pol,
		Model:        e.cfg.Model,
		SystemPrompt: sysPrompt,
		MaxTokens:    maxTokens,
		MaxSteps:     e.cfg.MaxSteps,
		SessionID:    sessionID,
		Skills:       skills,
		NoTool:       noTool,
		AutoAccept:   autoAccept,
		Out:          out,
		OnStep:       func(msgs []provider.Message) { sess.AppendMessages(msgs) },
	}

	_, usage, allMsgs, err := a.RunMessages(ctx, sess.Messages)
	if sr != nil {
		sr.Finish()
	}

	if err != nil {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, styleErr.Render("error: "+err.Error()))
		_, exitCode := agent.Status(err)
		os.Exit(exitCode)
	}

	if jsonMode {
		json.NewEncoder(os.Stdout).Encode(jsonResult{
			SessionID: sessionID,
			Status:    "ok",
			Context:   sess.FilePath,
			Text:      lastAssistantText(allMsgs),
			Usage:     jsonUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens},
		})
	} else {
		fmt.Fprintf(os.Stderr, "\n%s\n",
			styleDim.Render(fmt.Sprintf("tokens: %d in / %d out", usage.InputTokens, usage.OutputTokens)))
	}

	return nil
}

func lastAssistantText(msgs []provider.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == provider.RoleAssistant {
			return msgs[i].Content
		}
	}
	return ""
}

func parseRenderStyle(flag string) render.Style {
	switch flag {
	case "auto":
		return render.StyleAuto
	case "dark":
		return render.StyleDark
	case "light":
		return render.StyleLight
	default:
		return ""
	}
}

func makeOutput(style render.Style) (io.Writer, *render.StreamRenderer) {
	if style != "" {
		sr, err := render.NewStreamRenderer(os.Stdout, style)
		if err != nil {
			fatal(err)
		}
		return sr, sr
	}
	return os.Stdout, nil
}

type jsonResult struct {
	SessionID string    `json:"session_id"`
	Status    string    `json:"status"`
	Context   string    `json:"context"`
	Text      string    `json:"text"`
	Usage     jsonUsage `json:"usage"`
}

type jsonUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
