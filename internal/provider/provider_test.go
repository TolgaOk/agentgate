package provider

import (
	"encoding/json"
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

func TestBashToolDefValidJSON(t *testing.T) {
	td := BashToolDef()

	if td.Name != "bash" {
		t.Errorf("Name = %q, want %q", td.Name, "bash")
	}
	if td.Description == "" {
		t.Error("Description is empty")
	}

	// Verify InputSchema is valid JSON.
	var schema map[string]any
	if err := json.Unmarshal(td.InputSchema, &schema); err != nil {
		t.Fatalf("InputSchema is not valid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("schema type = %v, want object", schema["type"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema missing properties")
	}
	if _, ok := props["command"]; !ok {
		t.Error("schema missing command property")
	}
}
