package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Provider     string   `toml:"provider"`
	Model        string   `toml:"model"`
	MaxTokens    int      `toml:"max_tokens"`
	Timeout      Duration `toml:"timeout"`
	RateLimitRPM int      `toml:"rate_limit_rpm"`
	RateLimitTPM int      `toml:"rate_limit_tpm"`
	BudgetDaily  float64  `toml:"budget_daily_usd"`
}

// Duration wraps time.Duration for TOML unmarshaling (e.g. "120s").
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalText(text []byte) error {
	var err error
	d.Duration, err = time.ParseDuration(string(text))
	return err
}

func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.Duration.String()), nil
}

// Defaults returns a Config with sensible defaults.
func Defaults() Config {
	return Config{
		Provider:     "anthropic",
		Model:        "claude-sonnet-4-6",
		MaxTokens:    8192,
		Timeout:      Duration{120 * time.Second},
		RateLimitRPM: 50,
		RateLimitTPM: 80000,
		BudgetDaily:  10.0,
	}
}

// Load reads config from path, applies defaults for missing fields,
// then applies env var overrides.
func Load(path string) (Config, error) {
	cfg := Defaults()
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	applyEnvOverrides(&cfg)
	return cfg, nil
}

// LoadDefault loads from the standard config location
// (~/.config/agentgate/config.toml). If the file doesn't exist,
// returns defaults with env overrides applied.
func LoadDefault() (Config, error) {
	path, err := defaultPath()
	if err != nil {
		return Config{}, err
	}
	cfg := Defaults()
	if _, err := os.Stat(path); err == nil {
		if _, err := toml.DecodeFile(path, &cfg); err != nil {
			return Config{}, fmt.Errorf("config: %w", err)
		}
	}
	applyEnvOverrides(&cfg)
	return cfg, nil
}

// Dir returns the agentgate config directory (~/.config/agentgate).
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	return filepath.Join(home, ".config", "agentgate"), nil
}

func defaultPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("AG_PROVIDER"); v != "" {
		cfg.Provider = v
	}
	if v := os.Getenv("AG_MODEL"); v != "" {
		cfg.Model = v
	}
}

// APIKeyEnvVar returns the expected env var name for the configured provider.
func (c Config) APIKeyEnvVar() string {
	switch c.Provider {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "openrouter":
		return "OPENROUTER_API_KEY"
	default:
		return ""
	}
}

// APIKey returns the API key from the environment for the configured provider.
func (c Config) APIKey() string {
	return os.Getenv(c.APIKeyEnvVar())
}

// Validate checks that the config has valid values.
func (c Config) Validate() error {
	switch c.Provider {
	case "anthropic", "openai", "openrouter":
	default:
		return fmt.Errorf("config: unknown provider %q", c.Provider)
	}
	if c.APIKey() == "" {
		return fmt.Errorf("config: env var %s is not set", c.APIKeyEnvVar())
	}
	if c.MaxTokens <= 0 {
		return fmt.Errorf("config: max_tokens must be positive")
	}
	if c.Timeout.Duration <= 0 {
		return fmt.Errorf("config: timeout must be positive")
	}
	return nil
}
