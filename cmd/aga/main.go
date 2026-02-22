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
	"github.com/TolgaOk/agentgate/internal/auth"
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
		Use:                "aga",
		Short:              "AgentGate Hub",
		Version:            "0.1.0-alpha",
		CompletionOptions:  cobra.CompletionOptions{DisableDefaultCmd: true},
	}

	askCmd := &cobra.Command{
		Use:   "ask [prompt]",
		Short: "Run a prompt (conversation saved to context file)",
		Args:  cobra.ArbitraryArgs,
		RunE:  runAsk,
	}
	askCmd.Flags().Bool("json", false, "JSON output to stdout")
	askCmd.Flags().String("context", "", "Path to conversation context file (JSONL); created if missing")
	askCmd.Flags().String("model", "", "Override model name")
	askCmd.Flags().String("system-prompt", "", "Path to system prompt file (.md)")
	askCmd.Flags().String("skill", "", "Path to directory of .md skill files to append to system prompt")
	askCmd.Flags().Int("max-tokens", 0, "Override max output tokens")
	askCmd.Flags().Duration("timeout", 0, "Overall timeout for the entire operation (e.g. 30s, 2m)")
	askCmd.Flags().String("session-id", "", "Tag this call for metrics tracking")
	askCmd.Flags().String("render", "", "Render markdown: auto, dark, light, raw")

	metricsCmd := &cobra.Command{
		Use:   "metrics",
		Short: "Show usage metrics",
		RunE:  runMetrics,
	}
	metricsCmd.Flags().String("since", "today", "Show usage since: today, 7d, 30d, or YYYY-MM-DD")

	authCmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage provider authentication",
	}

	authOpenAICmd := &cobra.Command{
		Use:   "openai",
		Short: "Authenticate with OpenAI via OAuth (Sign in with ChatGPT)",
		RunE:  runAuthOpenAI,
	}

	authStatusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show authentication status for all providers",
		RunE:  runAuthStatus,
	}

	authCmd.AddCommand(authOpenAICmd, authStatusCmd)
	rootCmd.AddCommand(askCmd, metricsCmd, authCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
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
			styleDim.Render(fmt.Sprintf("tokens: %d in / %d out", usage.InputTokens, usage.OutputTokens)))
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
		GlobalLimit:   cfg.ConcurrentGlobalLimit,
		ProviderLimit: cfg.ConcurrentPerProviderLimit,
		ProviderName:  cfg.Provider,
		Model:         cfg.Model,
	}, store)

	return &env{cfg: cfg, pol: pol, store: store, provider: q}, nil
}

func newProvider(cfg config.Config) (provider.Provider, error) {
	apiKey := cfg.APIKey()
	oauthToken := false

	// For OpenAI, prefer subscription token (free via ChatGPT account)
	// over API key. Fall back to API key if no OAuth token exists.
	if cfg.Provider == "openai" {
		tok, err := loadOpenAIToken()
		if err == nil && tok != "" {
			apiKey = tok
			oauthToken = true
		}
	}

	if apiKey == "" {
		envHint := cfg.APIKeyEnvVar()
		if cfg.Provider == "openai" {
			return nil, fmt.Errorf("env var %s is not set and no OAuth token found (run: ag auth openai)", envHint)
		}
		return nil, fmt.Errorf("env var %s is not set", envHint)
	}

	switch cfg.Provider {
	case "anthropic":
		if strings.HasPrefix(apiKey, "sk-ant-oat") {
			return provider.NewAnthropicBearer(apiKey, cfg.Model, cfg.MaxTokens), nil
		}
		return provider.NewAnthropic(apiKey, cfg.Model, cfg.MaxTokens), nil
	case "openai":
		baseURL := provider.OpenAIResponsesAPI
		if oauthToken {
			baseURL = provider.CodexResponsesAPI
		}
		return provider.NewOpenAI(apiKey, baseURL, cfg.Model, cfg.MaxTokens), nil
	case "openrouter":
		return provider.NewOpenRouter(apiKey, cfg.Model, cfg.MaxTokens), nil
	default:
		return nil, fmt.Errorf("unknown provider %q", cfg.Provider)
	}
}

// loadOpenAIToken loads and auto-refreshes the OpenAI OAuth token.
func loadOpenAIToken() (string, error) {
	store, err := auth.LoadStore()
	if err != nil {
		return "", err
	}
	tok := store.Get("openai")
	if tok == nil {
		return "", fmt.Errorf("no openai token")
	}

	if tok.Expired() {
		if tok.RefreshToken == "" {
			return "", fmt.Errorf("openai token expired and no refresh token")
		}
		cfg := auth.OpenAIOAuth()
		newTok, err := auth.RefreshAccessToken(context.Background(), cfg, tok.RefreshToken)
		if err != nil {
			return "", fmt.Errorf("openai token refresh: %w", err)
		}
		if err := store.Set("openai", newTok); err != nil {
			return "", fmt.Errorf("save refreshed token: %w", err)
		}
		return newTok.AccessToken, nil
	}

	return tok.AccessToken, nil
}

func runAuthOpenAI(cmd *cobra.Command, args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg := auth.OpenAIOAuth()
	tok, err := auth.RunOAuthFlow(ctx, cfg)
	if err != nil {
		return fmt.Errorf("auth openai: %w", err)
	}

	store, err := auth.LoadStore()
	if err != nil {
		return fmt.Errorf("auth openai: %w", err)
	}
	if err := store.Set("openai", tok); err != nil {
		return fmt.Errorf("auth openai: %w", err)
	}

	fmt.Println("OpenAI authentication successful!")
	fmt.Printf("Token expires: %s\n", tok.ExpiresAt.Format(time.RFC3339))
	return nil
}

func runAuthStatus(cmd *cobra.Command, args []string) error {
	store, err := auth.LoadStore()
	if err != nil {
		return fmt.Errorf("auth status: %w", err)
	}

	providers := store.Providers()
	if len(providers) == 0 {
		fmt.Println("No OAuth tokens stored.")
		fmt.Println("Run 'ag auth openai' to authenticate with OpenAI.")
		return nil
	}

	for _, name := range providers {
		tok := store.Get(name)
		status := "valid"
		if tok.Expired() {
			if tok.RefreshToken != "" {
				status = "expired (has refresh token)"
			} else {
				status = "expired"
			}
		}
		fmt.Printf("%-12s %s  (expires %s)\n", name, status, tok.ExpiresAt.Format(time.RFC3339))
	}
	return nil
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

func runMetrics(cmd *cobra.Command, args []string) error {
	sinceFlag, _ := cmd.Flags().GetString("since")

	var since time.Time
	now := time.Now()
	switch sinceFlag {
	case "today", "":
		since = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	case "7d":
		since = now.AddDate(0, 0, -7)
	case "30d":
		since = now.AddDate(0, 0, -30)
	default:
		t, err := time.Parse("2006-01-02", sinceFlag)
		if err != nil {
			return fmt.Errorf("invalid --since value %q (use: today, 7d, 30d, or YYYY-MM-DD)", sinceFlag)
		}
		since = t
	}

	metricsDir, err := dataDir()
	if err != nil {
		return err
	}
	store, err := metrics.NewStore(filepath.Join(metricsDir, "metrics.db"))
	if err != nil {
		return err
	}
	defer store.Close()

	days, err := store.Summary(context.Background(), since)
	if err != nil {
		return err
	}

	if len(days) == 0 {
		fmt.Println("No usage data.")
		return nil
	}

	var totalIn, totalOut, totalCalls int
	for _, d := range days {
		fmt.Printf("%s  %6d in  %6d out  %3d calls\n", d.Date, d.InputTokens, d.OutputTokens, d.CallCount)
		totalIn += d.InputTokens
		totalOut += d.OutputTokens
		totalCalls += d.CallCount
	}
	if len(days) > 1 {
		fmt.Printf("%-10s %6d in  %6d out  %3d calls\n", "total", totalIn, totalOut, totalCalls)
	}

	return nil
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
