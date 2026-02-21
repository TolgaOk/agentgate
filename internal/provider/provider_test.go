package provider

import (
	"encoding/json"
	"testing"
)

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
