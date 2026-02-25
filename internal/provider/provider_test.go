package provider

import (
	"testing"
)

func TestParseOpenRouterError(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{
			name:   "valid error",
			status: 400,
			body:   `{"error":{"message":"bad-model is not a valid model ID","code":400}}`,
			want:   "openrouter: bad-model is not a valid model ID (HTTP 400)",
		},
		{
			name:   "auth error",
			status: 401,
			body:   `{"error":{"message":"Unauthorized","code":401}}`,
			want:   "openrouter: Unauthorized (HTTP 401)",
		},
		{
			name:   "unparseable body",
			status: 502,
			body:   `<html>bad gateway</html>`,
			want:   "openrouter: HTTP 502: <html>bad gateway</html>",
		},
		{
			name:   "empty message field",
			status: 500,
			body:   `{"error":{"code":500}}`,
			want:   `openrouter: HTTP 500: {"error":{"code":500}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := parseOpenRouterError(tt.status, []byte(tt.body))
			if err.Error() != tt.want {
				t.Errorf("got %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

func TestEnsureJSON_ValidJSON(t *testing.T) {
	input := `{"command":"ls -la"}`
	got := ensureJSON(input)
	if got != input {
		t.Errorf("ensureJSON(%q) = %q, want unchanged", input, got)
	}
}

func TestEnsureJSON_PlainString(t *testing.T) {
	got := ensureJSON("ls -la")
	want := `{"command":"ls -la"}`
	if got != want {
		t.Errorf("ensureJSON(plain) = %q, want %q", got, want)
	}
}
