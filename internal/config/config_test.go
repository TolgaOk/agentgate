package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte(`
provider = "openai"
model = "gpt-4o"
max_tokens = 4096
timeout = "60s"
concurrent_global_limit = 5
concurrent_per_provider_limit = 2
`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	want := Config{
		Provider:                   "openai",
		Model:                      "gpt-4o",
		MaxTokens:                  4096,
		MaxSteps:                   20,
		ConcurrentGlobalLimit:      5,
		ConcurrentPerProviderLimit: 2,
		PolicyConfig: PolicyConfig{
			Timeout: "30s",
			Allowed: []string{},
			Blocked: []string{},
		},
	}
	if diff := cmp.Diff(want, cfg); diff != "" {
		t.Errorf("Load() mismatch (-want +got):\n%s", diff)
	}
}

func TestDefaultsApplied(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte(`provider = "openai"`), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Provider != "openai" {
		t.Errorf("Provider = %q, want openai", cfg.Provider)
	}
	if cfg.MaxTokens != 8192 {
		t.Errorf("MaxTokens = %d, want 8192 (default)", cfg.MaxTokens)
	}
}

func TestEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte(`provider = "anthropic"`), 0644)

	t.Setenv("AG_PROVIDER", "openai")
	t.Setenv("AG_MODEL", "gpt-4o-mini")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Provider != "openai" {
		t.Errorf("Provider = %q, want openai (from AG_PROVIDER)", cfg.Provider)
	}
	if cfg.Model != "gpt-4o-mini" {
		t.Errorf("Model = %q, want gpt-4o-mini (from AG_MODEL)", cfg.Model)
	}
}

func TestAPIKeyEnvVar(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"anthropic", "ANTHROPIC_API_KEY"},
		{"openai", "OPENAI_API_KEY"},
		{"openrouter", "OPENROUTER_API_KEY"},
		{"unknown", ""},
	}
	for _, tt := range tests {
		cfg := Defaults()
		cfg.Provider = tt.provider
		if got := cfg.APIKeyEnvVar(); got != tt.want {
			t.Errorf("APIKeyEnvVar(%s) = %q, want %q", tt.provider, got, tt.want)
		}
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		setEnv  map[string]string
		wantErr bool
	}{
		{
			name:    "valid",
			modify:  func(c *Config) { c.Provider = "anthropic" },
			setEnv:  map[string]string{"ANTHROPIC_API_KEY": "test-key"},
			wantErr: false,
		},
		{
			name:    "unknown provider",
			modify:  func(c *Config) { c.Provider = "gemini" },
			wantErr: true,
		},
		{
			name:    "api key not set",
			modify:  func(c *Config) { c.Provider = "anthropic" },
			wantErr: true,
		},
		{
			name:    "zero max_tokens",
			modify:  func(c *Config) { c.MaxTokens = 0 },
			setEnv:  map[string]string{"ANTHROPIC_API_KEY": "test-key"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.setEnv {
				t.Setenv(k, v)
			}
			cfg := Defaults()
			if tt.modify != nil {
				tt.modify(&cfg)
			}
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.toml")
	if err == nil {
		t.Error("Load() should fail for missing file")
	}
}

