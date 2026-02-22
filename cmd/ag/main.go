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
	styleTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	styleDim   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleErr   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "ask":
		cmdAsk(os.Args[2:])
	case "metrics":
		fmt.Fprintln(os.Stderr, styleErr.Render("ag metrics: not implemented"))
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "%s %s\n", styleErr.Render("unknown command:"), os.Args[1])
		printUsage()
		os.Exit(2)
	}
}

// env holds shared components initialized by setupEnv.
type env struct {
	cfg      config.Config
	pol      policy.Policy
	store    *metrics.Store
	provider provider.Provider
}

// setupEnv loads config, policy, prompt, metrics, and creates the provider + queue.
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

	return &env{
		cfg:      cfg,
		pol:      pol,
		store:    store,
		provider: q,
	}, nil
}

// loadSkillDir reads all .md files from dir and concatenates them.
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

// lastAssistantText returns the text of the last assistant message.
func lastAssistantText(msgs []provider.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == provider.RoleAssistant {
			return msgs[i].Content
		}
	}
	return ""
}

// makeOutput creates the output writer, optionally wrapping in a StreamRenderer.
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

// askFlags holds all parsed flags for the ask command.
type askFlags struct {
	jsonMode     bool
	renderStyle  render.Style
	model        string
	systemPrompt string
	maxTokens    int
	timeout      time.Duration
	skillDir     string
	contextFile  string
	sessionID    string
	prompt       string
}

func parseAskFlags(args []string) askFlags {
	var f askFlags
	var rest []string

	nextVal := func(i *int, flag string) string {
		*i++
		if *i >= len(args) {
			fmt.Fprintf(os.Stderr, "%s requires a value\n", flag)
			os.Exit(2)
		}
		return args[*i]
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			f.jsonMode = true
		case "--render":
			f.renderStyle = render.StyleAuto
		case "--dark":
			f.renderStyle = render.StyleDark
		case "--light":
			f.renderStyle = render.StyleLight
		case "--raw":
			f.renderStyle = ""
		case "--model":
			f.model = nextVal(&i, "--model")
		case "--system-prompt":
			f.systemPrompt = nextVal(&i, "--system-prompt")
		case "--max-tokens":
			v := nextVal(&i, "--max-tokens")
			fmt.Sscanf(v, "%d", &f.maxTokens)
		case "--timeout":
			v := nextVal(&i, "--timeout")
			d, err := time.ParseDuration(v)
			if err == nil {
				f.timeout = d
			}
		case "--skill":
			f.skillDir = nextVal(&i, "--skill")
		case "--context":
			f.contextFile = nextVal(&i, "--context")
		case "--session-id":
			f.sessionID = nextVal(&i, "--session-id")
		default:
			rest = append(rest, args[i])
		}
	}

	// Build prompt: args first, stdin only when piped and no args given.
	f.prompt = strings.Join(rest, " ")
	if f.prompt == "" && stdinPiped() {
		data, err := io.ReadAll(os.Stdin)
		if err == nil {
			f.prompt = strings.TrimSpace(string(data))
		}
	}

	return f
}

func cmdAsk(args []string) {
	if hasHelp(args) {
		fmt.Fprintln(os.Stderr, styleTitle.Render("ag ask")+" "+styleDim.Render("— run a prompt"))
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Usage: ag ask [flags] \"prompt\"")
		fmt.Fprintln(os.Stderr, "       echo \"prompt\" | ag ask [flags]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Flags:")
		fmt.Fprintln(os.Stderr, "  --json                JSON output to stdout")
		fmt.Fprintln(os.Stderr, "  --context FILE        Load and extend conversation context (JSONL)")
		fmt.Fprintln(os.Stderr, "  --model MODEL         Override model")
		fmt.Fprintln(os.Stderr, "  --system-prompt FILE  Override system prompt")
		fmt.Fprintln(os.Stderr, "  --skill DIR           Append .md files from dir to system prompt")
		fmt.Fprintln(os.Stderr, "  --max-tokens N        Override max output tokens")
		fmt.Fprintln(os.Stderr, "  --timeout DURATION    Override command timeout (e.g. 30s)")
		fmt.Fprintln(os.Stderr, "  --session-id ID       Tag this call for metrics tracking")
		fmt.Fprintln(os.Stderr, "  --render/--dark/--light/--raw  Markdown rendering")
		return
	}
	f := parseAskFlags(args)

	if f.prompt == "" {
		fmt.Fprintln(os.Stderr, styleErr.Render("usage: ag ask [flags] \"prompt\""))
		os.Exit(2)
	}
	if strings.TrimSpace(f.prompt) == "" {
		fmt.Fprintln(os.Stderr, styleDim.Render("nothing to do"))
		return
	}

	e, err := setupEnv()
	if err != nil {
		fatal(err)
	}
	defer e.store.Close()

	// Apply per-invocation overrides.
	if f.model != "" {
		e.cfg.Model = f.model
		p, err := newProvider(e.cfg)
		if err != nil {
			fatal(err)
		}
		e.provider = queue.New(p, queue.Config{
			RPM:    e.cfg.RateLimitRPM,
			Budget: e.cfg.BudgetDaily,
		}, e.store)
	}

	// Load system prompt: --system-prompt overrides default.
	var sysPrompt string
	if f.systemPrompt != "" {
		data, err := os.ReadFile(f.systemPrompt)
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

	// Append skill docs from --skill directory.
	if f.skillDir != "" {
		extra, err := loadSkillDir(f.skillDir)
		if err != nil {
			fatal(err)
		}
		if extra != "" {
			sysPrompt = sysPrompt + "\n\n" + extra
		}
	}

	maxTokens := e.cfg.MaxTokens
	if f.maxTokens > 0 {
		maxTokens = f.maxTokens
	}

	if f.timeout > 0 {
		e.pol.Timeout = f.timeout
	}

	// Open or create context file.
	var sess *session.Session
	if f.contextFile != "" {
		sess, err = session.Open(f.contextFile)
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

	// Session ID: use provided or generate.
	sessionID := f.sessionID
	if sessionID == "" {
		sessionID = sess.ID
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Append user prompt to context.
	userMsg := provider.Message{
		Role:    provider.RoleUser,
		Content: f.prompt,
		Meta:    map[string]string{"date": time.Now().UTC().Format(time.RFC3339)},
	}
	if err := sess.AppendMessage(userMsg); err != nil {
		fatal(err)
	}

	if f.jsonMode {
		a := &agent.Agent{
			Provider:     e.provider,
			ProviderName: e.cfg.Provider,
			Policy:       e.pol,
			Metrics:      e.store,
			Model:        e.cfg.Model,
			SystemPrompt: sysPrompt,
			MaxTokens:    maxTokens,
			SessionID:    sessionID,
			Out:          io.Discard,
		}
		prevLen := len(sess.Messages)
		_, usage, allMsgs, err := a.RunMessages(ctx, sess.Messages)
		sess.AppendMessages(allMsgs[prevLen:])
		if err != nil {
			fatal(err)
		}
		json.NewEncoder(os.Stdout).Encode(jsonResult{
			SessionID: sessionID,
			Status:    "ok",
			Context:   sess.FilePath,
			Text:      lastAssistantText(allMsgs),
			Usage:     jsonUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens},
		})
		return
	}

	out, sr := makeOutput(f.renderStyle)
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
	// Persist new messages to context file.
	sess.AppendMessages(allMsgs[prevLen:])

	if err != nil {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, styleErr.Render("error: "+err.Error()))
		_, exitCode := agent.Status(err)
		os.Exit(exitCode)
	}

	fmt.Fprintf(os.Stderr, "\n%s\n",
		styleDim.Render(fmt.Sprintf("tokens: %d in / %d out  context: %s", usage.InputTokens, usage.OutputTokens, sess.FilePath)))
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

func hasHelp(args []string) bool {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			return true
		}
	}
	return false
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

func printUsage() {
	fmt.Fprintln(os.Stderr, styleTitle.Render("ag")+" "+styleDim.Render("— AgentGate Hub"))
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, styleTitle.Render("Usage:")+" ag ask [flags] \"prompt\"")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  ask      Run a prompt (conversation saved to context file)")
	fmt.Fprintln(os.Stderr, "  metrics  Show usage metrics")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Flags:")
	fmt.Fprintln(os.Stderr, "  --json                JSON output to stdout")
	fmt.Fprintln(os.Stderr, "  --context FILE        Load and extend conversation context (JSONL)")
	fmt.Fprintln(os.Stderr, "  --model MODEL         Override model")
	fmt.Fprintln(os.Stderr, "  --system-prompt FILE  Override system prompt")
	fmt.Fprintln(os.Stderr, "  --skill DIR           Append .md files from dir to system prompt")
	fmt.Fprintln(os.Stderr, "  --max-tokens N        Override max output tokens")
	fmt.Fprintln(os.Stderr, "  --timeout DURATION    Override command timeout (e.g. 30s)")
	fmt.Fprintln(os.Stderr, "  --session-id ID       Tag this call for metrics tracking")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Render flags:")
	fmt.Fprintln(os.Stderr, "  --render  Render markdown (auto-detect bg)")
	fmt.Fprintln(os.Stderr, "  --dark    Render markdown (dark background)")
	fmt.Fprintln(os.Stderr, "  --light   Render markdown (light background)")
	fmt.Fprintln(os.Stderr, "  --raw     Raw text output (default)")
}
