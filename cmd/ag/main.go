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

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/TolgaOk/agentgate/internal/agent"
	"github.com/TolgaOk/agentgate/internal/config"
	"github.com/TolgaOk/agentgate/internal/metrics"
	"github.com/TolgaOk/agentgate/internal/policy"
	"github.com/TolgaOk/agentgate/internal/prompt"
	"github.com/TolgaOk/agentgate/internal/provider"
	"github.com/TolgaOk/agentgate/internal/queue"
	"github.com/TolgaOk/agentgate/internal/render"
	"github.com/TolgaOk/agentgate/internal/session"
)

var (
	styleDim = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleErr = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "ag",
		Short: "AgentGate Hub",
	}

	askCmd := &cobra.Command{
		Use:   "ask [prompt]",
		Short: "Run a prompt (conversation saved to context file)",
		Args:  cobra.ArbitraryArgs,
		RunE:  runAsk,
	}
	askCmd.Flags().Bool("json", false, "JSON output to stdout")
	askCmd.Flags().String("context", "", "Load and extend conversation context (JSONL)")
	askCmd.Flags().String("model", "", "Override model")
	askCmd.Flags().String("system-prompt", "", "Override system prompt file")
	askCmd.Flags().String("skill", "", "Append .md files from dir to system prompt")
	askCmd.Flags().Int("max-tokens", 0, "Override max output tokens")
	askCmd.Flags().Duration("timeout", 0, "Override command timeout (e.g. 30s)")
	askCmd.Flags().String("session-id", "", "Tag this call for metrics tracking")
	askCmd.Flags().String("render", "", "Render markdown: auto, dark, light, raw")

	metricsCmd := &cobra.Command{
		Use:   "metrics",
		Short: "Show usage metrics",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("not implemented")
		},
	}

	rootCmd.AddCommand(askCmd, metricsCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runAsk(cmd *cobra.Command, args []string) error {
	// Build prompt from args or stdin.
	userPrompt := strings.Join(args, " ")
	if userPrompt == "" && stdinPiped() {
		data, err := io.ReadAll(os.Stdin)
		if err == nil {
			userPrompt = strings.TrimSpace(string(data))
		}
	}
	if strings.TrimSpace(userPrompt) == "" {
		return fmt.Errorf("usage: ag ask [flags] \"prompt\"")
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
			RPM:    e.cfg.RateLimitRPM,
			Budget: e.cfg.BudgetDaily,
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

	// Append skill docs.
	if skillDir != "" {
		extra, err := loadSkillDir(skillDir)
		if err != nil {
			fatal(err)
		}
		if extra != "" {
			sysPrompt = sysPrompt + "\n\n" + extra
		}
	}

	maxTokens := e.cfg.MaxTokens
	if maxTokensFlag > 0 {
		maxTokens = maxTokensFlag
	}

	if timeoutFlag > 0 {
		e.pol.Timeout = timeoutFlag
	}

	// Open or create context file.
	var sess *session.Session
	if contextFile != "" {
		sess, err = session.Open(contextFile)
		if err != nil {
			fatal(err)
		}
	} else {
		ctxDir := filepath.Join(os.TempDir(), "agentgate")
		sess, err = session.New(ctxDir, e.cfg.Provider+"/"+e.cfg.Model)
		if err != nil {
			fatal(err)
		}
	}
	defer sess.Close()

	if sessionID == "" {
		sessionID = sess.ID
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

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
		Metrics:      e.store,
		Model:        e.cfg.Model,
		SystemPrompt: sysPrompt,
		MaxTokens:    maxTokens,
		SessionID:    sessionID,
		Out:          out,
	}

	prevLen := len(sess.Messages)
	_, usage, allMsgs, err := a.RunMessages(ctx, sess.Messages)
	if sr != nil {
		sr.Finish()
	}
	sess.AppendMessages(allMsgs[prevLen:])

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
			styleDim.Render(fmt.Sprintf("tokens: %d in / %d out  context: %s", usage.InputTokens, usage.OutputTokens, sess.FilePath)))
	}

	return nil
}

// --- helpers ---

type env struct {
	cfg      config.Config
	pol      policy.Policy
	store    *metrics.Store
	provider provider.Provider
}

func setupEnv() (*env, error) {
	cfg, err := config.LoadDefault()
	if err != nil {
		return nil, err
	}

	pol, err := policy.LoadDefault()
	if err != nil {
		return nil, err
	}

	metricsDir, err := dataDir()
	if err != nil {
		return nil, err
	}
	os.MkdirAll(metricsDir, 0755)
	store, err := metrics.NewStore(filepath.Join(metricsDir, "metrics.db"))
	if err != nil {
		return nil, err
	}

	p, err := newProvider(cfg)
	if err != nil {
		store.Close()
		return nil, err
	}

	q := queue.New(p, queue.Config{
		RPM:    cfg.RateLimitRPM,
		Budget: cfg.BudgetDaily,
	}, store)

	return &env{cfg: cfg, pol: pol, store: store, provider: q}, nil
}

func newProvider(cfg config.Config) (provider.Provider, error) {
	apiKey := cfg.APIKey()
	if apiKey == "" {
		return nil, fmt.Errorf("env var %s is not set", cfg.APIKeyEnvVar())
	}

	switch cfg.Provider {
	case "anthropic":
		return provider.NewAnthropic(apiKey, cfg.Model, cfg.MaxTokens), nil
	case "openrouter":
		return provider.NewOpenRouter(apiKey, cfg.Model, cfg.MaxTokens), nil
	default:
		return nil, fmt.Errorf("unknown provider %q", cfg.Provider)
	}
}

func loadSkillDir(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("skill dir: %w", err)
	}
	var parts []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return "", fmt.Errorf("skill dir: %w", err)
		}
		parts = append(parts, strings.TrimSpace(string(data)))
	}
	return strings.Join(parts, "\n\n"), nil
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

func stdinPiped() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice == 0
}

func dataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "agentgate"), nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, styleErr.Render("error: "+err.Error()))
	os.Exit(1)
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
