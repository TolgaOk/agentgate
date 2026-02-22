package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// OAuthConfig holds the OAuth 2.1 parameters for a provider.
type OAuthConfig struct {
	AuthURL     string
	TokenURL    string
	ClientID    string
	Scopes      []string
	ExtraParams map[string]string // additional query params for the authorize URL
}

// OpenAIOAuth returns the OAuth config for OpenAI "Sign in with ChatGPT".
func OpenAIOAuth() OAuthConfig {
	return OAuthConfig{
		AuthURL:  "https://auth.openai.com/oauth/authorize",
		TokenURL: "https://auth.openai.com/oauth/token",
		ClientID: "app_EMoamEEZ73f0CkXaXp7hrann",
		Scopes:   []string{"openid", "profile", "email", "offline_access"},
		ExtraParams: map[string]string{
			"id_token_add_organizations":  "true",
			"codex_cli_simplified_flow":   "true",
		},
	}
}

// RunOAuthFlow runs a full OAuth 2.1 + PKCE browser flow.
// It starts a local HTTP server, opens the browser, waits for the callback,
// exchanges the code for tokens, and returns them.
func RunOAuthFlow(ctx context.Context, cfg OAuthConfig) (*Token, error) {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return nil, fmt.Errorf("oauth: pkce: %w", err)
	}

	// Listen on port 1455 — the registered redirect URI for the Codex public client.
	listener, err := net.Listen("tcp", "127.0.0.1:1455")
	if err != nil {
		return nil, fmt.Errorf("oauth: listen on :1455 (is another Codex/ag instance running?): %w", err)
	}
	redirectURI := "http://localhost:1455/auth/callback"

	// Build authorization URL.
	state, err := randomString(32)
	if err != nil {
		listener.Close()
		return nil, fmt.Errorf("oauth: state: %w", err)
	}

	authURL := buildAuthURL(cfg, redirectURI, state, challenge)

	// Channel to receive the authorization code.
	type callbackResult struct {
		code string
		err  error
	}
	resultCh := make(chan callbackResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		if errMsg := r.URL.Query().Get("error"); errMsg != "" {
			desc := r.URL.Query().Get("error_description")
			resultCh <- callbackResult{err: fmt.Errorf("oauth: %s: %s", errMsg, desc)}
			fmt.Fprintf(w, "<html><body><h2>Authentication failed</h2><p>%s</p><p>You can close this tab.</p></body></html>", desc)
			return
		}

		if got := r.URL.Query().Get("state"); got != state {
			resultCh <- callbackResult{err: fmt.Errorf("oauth: state mismatch")}
			fmt.Fprint(w, "<html><body><h2>State mismatch</h2><p>You can close this tab.</p></body></html>")
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			resultCh <- callbackResult{err: fmt.Errorf("oauth: no code in callback")}
			fmt.Fprint(w, "<html><body><h2>No code received</h2><p>You can close this tab.</p></body></html>")
			return
		}

		resultCh <- callbackResult{code: code}
		fmt.Fprint(w, "<html><body><h2>Authenticated!</h2><p>You can close this tab and return to the terminal.</p></body></html>")
	})

	server := &http.Server{Handler: mux}
	go server.Serve(listener)
	defer server.Close()

	// Open browser.
	fmt.Printf("Opening browser for authentication...\n")
	fmt.Printf("If the browser doesn't open, visit:\n%s\n\n", authURL)
	openBrowser(authURL)

	// Wait for callback or context cancellation.
	var result callbackResult
	select {
	case result = <-resultCh:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if result.err != nil {
		return nil, result.err
	}

	// Exchange code for tokens.
	return exchangeCode(ctx, cfg, result.code, redirectURI, verifier)
}

// RefreshAccessToken uses a refresh token to get a new access token.
func RefreshAccessToken(ctx context.Context, cfg OAuthConfig, refreshToken string) (*Token, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {cfg.ClientID},
		"refresh_token": {refreshToken},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", cfg.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("oauth: refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: refresh do: %w", err)
	}
	defer resp.Body.Close()

	return parseTokenResponse(resp)
}

func exchangeCode(ctx context.Context, cfg OAuthConfig, code, redirectURI, verifier string) (*Token, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {cfg.ClientID},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", cfg.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("oauth: token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: token do: %w", err)
	}
	defer resp.Body.Close()

	return parseTokenResponse(resp)
}

func parseTokenResponse(resp *http.Response) (*Token, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("oauth: read token response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("oauth: token endpoint HTTP %d: %s", resp.StatusCode, body)
	}

	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		TokenType    string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("oauth: parse token: %w", err)
	}

	if tr.AccessToken == "" {
		return nil, fmt.Errorf("oauth: empty access token in response: %s", body)
	}

	expiresAt := time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return &Token{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		ExpiresAt:    expiresAt,
	}, nil
}

// --- PKCE ---

func generatePKCE() (verifier, challenge string, err error) {
	verifier, err = randomString(64)
	if err != nil {
		return "", "", err
	}
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return verifier, challenge, nil
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b)[:n], nil
}

func buildAuthURL(cfg OAuthConfig, redirectURI, state, challenge string) string {
	v := url.Values{
		"response_type":         {"code"},
		"client_id":             {cfg.ClientID},
		"redirect_uri":          {redirectURI},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	if len(cfg.Scopes) > 0 {
		v.Set("scope", strings.Join(cfg.Scopes, " "))
	}
	for k, val := range cfg.ExtraParams {
		v.Set(k, val)
	}
	return cfg.AuthURL + "?" + v.Encode()
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		return
	}
	cmd.Start()
}
