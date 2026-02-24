package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/TolgaOk/agentgate/internal/auth"
	"github.com/TolgaOk/agentgate/internal/config"
	"github.com/TolgaOk/agentgate/internal/metrics"
	"github.com/TolgaOk/agentgate/internal/policy"
	"github.com/TolgaOk/agentgate/internal/provider"
	"github.com/TolgaOk/agentgate/internal/queue"
)

var (
	styleDim = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleErr = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
)

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
