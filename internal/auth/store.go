package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Token holds OAuth tokens for a provider.
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// Expired reports whether the token has expired (with a 60s buffer).
func (t Token) Expired() bool {
	return time.Now().After(t.ExpiresAt.Add(-60 * time.Second))
}

// Store manages OAuth tokens on disk at ~/.config/agentgate/tokens.json.
type Store struct {
	path   string
	tokens map[string]*Token
}

// DefaultStorePath returns ~/.config/agentgate/tokens.json.
func DefaultStorePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("auth store: %w", err)
	}
	return filepath.Join(home, ".config", "agentgate", "tokens.json"), nil
}

// LoadStore reads the token store from the default path.
// Returns an empty store if the file doesn't exist.
func LoadStore() (*Store, error) {
	path, err := DefaultStorePath()
	if err != nil {
		return nil, err
	}
	return LoadStoreFrom(path)
}

// LoadStoreFrom reads the token store from the given path.
func LoadStoreFrom(path string) (*Store, error) {
	s := &Store{path: path, tokens: make(map[string]*Token)}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth store: read: %w", err)
	}
	if err := json.Unmarshal(data, &s.tokens); err != nil {
		return nil, fmt.Errorf("auth store: parse: %w", err)
	}
	return s, nil
}

// Get returns the token for a provider, or nil if not found.
func (s *Store) Get(provider string) *Token {
	return s.tokens[provider]
}

// Set stores a token for a provider and writes to disk.
func (s *Store) Set(provider string, tok *Token) error {
	s.tokens[provider] = tok
	return s.save()
}

// Delete removes a provider's token and writes to disk.
func (s *Store) Delete(provider string) error {
	delete(s.tokens, provider)
	return s.save()
}

// Providers returns the names of all stored providers.
func (s *Store) Providers() []string {
	var names []string
	for k := range s.tokens {
		names = append(names, k)
	}
	return names
}

func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("auth store: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(s.tokens, "", "  ")
	if err != nil {
		return fmt.Errorf("auth store: marshal: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0600); err != nil {
		return fmt.Errorf("auth store: write: %w", err)
	}
	return nil
}
