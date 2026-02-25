package skill

import (
	"encoding/json"
	"testing"
)

func TestToToolDef_Bash(t *testing.T) {
	s := Skill{
		Name:        "bash",
		Description: "Run a shell command",
		Tool: &ToolMeta{
			Command: "sh",
			Args: map[string]ArgDef{
				"command": {Type: "string", Required: true, Flag: "-c", Desc: "The shell command to execute"},
			},
		},
	}

	td := s.ToToolDef()
	if td.Name != "bash" {
		t.Errorf("Name = %q", td.Name)
	}
	if td.Description != "Run a shell command" {
		t.Errorf("Description = %q", td.Description)
	}

	// Verify schema roundtrip.
	var schema struct {
		Type       string                       `json:"type"`
		Properties map[string]map[string]string `json:"properties"`
		Required   []string                     `json:"required"`
	}
	if err := json.Unmarshal(td.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Type != "object" {
		t.Errorf("schema type = %q", schema.Type)
	}
	cmd, ok := schema.Properties["command"]
	if !ok {
		t.Fatal("missing command property")
	}
	if cmd["type"] != "string" {
		t.Errorf("command type = %q", cmd["type"])
	}
	if len(schema.Required) != 1 || schema.Required[0] != "command" {
		t.Errorf("required = %v", schema.Required)
	}
}

func TestToToolDef_Grep(t *testing.T) {
	s := Skill{
		Name:        "grep",
		Description: "Search files",
		Tool: &ToolMeta{
			Command: "rg",
			Args: map[string]ArgDef{
				"pattern": {Type: "string", Required: true, Position: 1, Desc: "regex pattern"},
				"path":    {Type: "string", Position: 2},
				"type":    {Type: "string", Flag: "--type"},
				"all":     {Type: "boolean", Flag: "-a"},
			},
		},
	}

	td := s.ToToolDef()
	var schema struct {
		Properties map[string]map[string]string `json:"properties"`
		Required   []string                     `json:"required"`
	}
	if err := json.Unmarshal(td.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Properties) != 4 {
		t.Errorf("len(properties) = %d, want 4", len(schema.Properties))
	}
	if len(schema.Required) != 1 || schema.Required[0] != "pattern" {
		t.Errorf("required = %v, want [pattern]", schema.Required)
	}
}

func TestBuildArgv_Bash(t *testing.T) {
	s := Skill{
		Name: "bash",
		Tool: &ToolMeta{
			Command: "sh",
			Args: map[string]ArgDef{
				"command": {Type: "string", Required: true, Flag: "-c"},
			},
		},
	}

	argv, err := s.BuildArgv(`{"command": "echo hello"}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"sh", "-c", "echo hello"}
	if !slicesEqual(argv, want) {
		t.Errorf("argv = %v, want %v", argv, want)
	}
}

func TestBuildArgv_Grep(t *testing.T) {
	s := Skill{
		Name: "grep",
		Tool: &ToolMeta{
			Command: "rg",
			Args: map[string]ArgDef{
				"pattern": {Type: "string", Required: true, Position: 1},
				"path":    {Type: "string", Position: 2},
				"type":    {Type: "string", Flag: "--type"},
			},
		},
	}

	argv, err := s.BuildArgv(`{"pattern": "TODO", "path": ".", "type": "go"}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"rg", "--type", "go", "TODO", "."}
	if !slicesEqual(argv, want) {
		t.Errorf("argv = %v, want %v", argv, want)
	}
}

func TestBuildArgv_BooleanFlag(t *testing.T) {
	s := Skill{
		Name: "ls",
		Tool: &ToolMeta{
			Command: "ls",
			Args: map[string]ArgDef{
				"all":  {Type: "boolean", Flag: "-a"},
				"path": {Type: "string", Position: 1},
			},
		},
	}

	// Boolean true → flag present.
	argv, err := s.BuildArgv(`{"all": true, "path": "/tmp"}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ls", "-a", "/tmp"}
	if !slicesEqual(argv, want) {
		t.Errorf("argv = %v, want %v", argv, want)
	}

	// Boolean false → flag omitted.
	argv, err = s.BuildArgv(`{"all": false, "path": "/tmp"}`)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"ls", "/tmp"}
	if !slicesEqual(argv, want) {
		t.Errorf("argv = %v, want %v", argv, want)
	}
}

func TestBuildArgv_MissingRequired(t *testing.T) {
	s := Skill{
		Name: "grep",
		Tool: &ToolMeta{
			Command: "rg",
			Args: map[string]ArgDef{
				"pattern": {Type: "string", Required: true, Position: 1},
			},
		},
	}

	_, err := s.BuildArgv(`{}`)
	if err == nil {
		t.Fatal("expected error for missing required arg")
	}
}

func TestBuildArgv_InvalidJSON(t *testing.T) {
	s := Skill{
		Name: "bash",
		Tool: &ToolMeta{Command: "sh", Args: map[string]ArgDef{}},
	}

	_, err := s.BuildArgv(`not json`)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
