package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.json")

	// Load from non-existent file — should return empty store.
	s, err := LoadStoreFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if tok := s.Get("openai"); tok != nil {
		t.Fatal("expected nil for missing provider")
	}

	// Set a token and verify it persists.
	tok := &Token{
		AccessToken:  "access-123",
		RefreshToken: "refresh-456",
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	if err := s.Set("openai", tok); err != nil {
		t.Fatal(err)
	}

	// File should exist now.
	if _, err := os.Stat(path); err != nil {
		t.Fatal("tokens.json not created")
	}

	// Reload from disk.
	s2, err := LoadStoreFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.Get("openai")
	if got == nil {
		t.Fatal("expected token after reload")
	}
	if got.AccessToken != "access-123" {
		t.Fatalf("access token = %q, want %q", got.AccessToken, "access-123")
	}
	if got.RefreshToken != "refresh-456" {
		t.Fatalf("refresh token = %q, want %q", got.RefreshToken, "refresh-456")
	}

	// Providers list.
	providers := s2.Providers()
	if len(providers) != 1 || providers[0] != "openai" {
		t.Fatalf("providers = %v, want [openai]", providers)
	}

	// Delete.
	if err := s2.Delete("openai"); err != nil {
		t.Fatal(err)
	}
	if tok := s2.Get("openai"); tok != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestTokenExpired(t *testing.T) {
	fresh := Token{ExpiresAt: time.Now().Add(5 * time.Minute)}
	if fresh.Expired() {
		t.Fatal("token with 5 min left should not be expired")
	}

	stale := Token{ExpiresAt: time.Now().Add(-1 * time.Minute)}
	if !stale.Expired() {
		t.Fatal("token expired 1 min ago should be expired")
	}

	// Within 60s buffer.
	borderline := Token{ExpiresAt: time.Now().Add(30 * time.Second)}
	if !borderline.Expired() {
		t.Fatal("token within 60s buffer should be expired")
	}
}
