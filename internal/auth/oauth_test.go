package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
)

func TestGeneratePKCE(t *testing.T) {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		t.Fatal(err)
	}
	if len(verifier) != 64 {
		t.Fatalf("verifier length = %d, want 64", len(verifier))
	}

	// Verify the challenge matches the verifier.
	h := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(h[:])
	if challenge != want {
		t.Fatalf("challenge mismatch:\ngot  %s\nwant %s", challenge, want)
	}
}

func TestBuildAuthURL(t *testing.T) {
	cfg := OAuthConfig{
		AuthURL:  "https://auth.example.com/authorize",
		ClientID: "test-client",
		Scopes:   []string{"openid", "profile"},
	}

	u := buildAuthURL(cfg, "http://127.0.0.1:8080/callback", "test-state", "test-challenge")

	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u, cfg.AuthURL) {
		t.Fatalf("URL doesn't start with auth URL: %s", u)
	}

	q := parsed.Query()
	if q.Get("response_type") != "code" {
		t.Fatalf("response_type = %q", q.Get("response_type"))
	}
	if q.Get("client_id") != "test-client" {
		t.Fatalf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("redirect_uri") != "http://127.0.0.1:8080/callback" {
		t.Fatalf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	if q.Get("state") != "test-state" {
		t.Fatalf("state = %q", q.Get("state"))
	}
	if q.Get("code_challenge") != "test-challenge" {
		t.Fatalf("code_challenge = %q", q.Get("code_challenge"))
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Fatalf("code_challenge_method = %q", q.Get("code_challenge_method"))
	}
	if q.Get("scope") != "openid profile" {
		t.Fatalf("scope = %q", q.Get("scope"))
	}
}

func TestRandomString(t *testing.T) {
	s1, err := randomString(32)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := randomString(32)
	if err != nil {
		t.Fatal(err)
	}
	if len(s1) != 32 {
		t.Fatalf("length = %d, want 32", len(s1))
	}
	if s1 == s2 {
		t.Fatal("two random strings should differ")
	}
}
