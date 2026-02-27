package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseToolSkill(t *testing.T) {
	content := `---
name: grep
description: Search file contents
metadata:
  command: rg
  args:
    pattern:
      type: string
      required: true
      position: 1
      desc: regex pattern
    path:
      type: string
      position: 2
    type:
      type: string
      flag: "--type"
---
Use grep to search files.
`
	s, err := Parse(content, "grep.md")
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "grep" {
		t.Errorf("Name = %q, want grep", s.Name)
	}
	if s.Description != "Search file contents" {
		t.Errorf("Description = %q", s.Description)
	}
	if s.Body != "Use grep to search files." {
		t.Errorf("Body = %q", s.Body)
	}
	if s.Tool == nil {
		t.Fatal("Tool is nil, want ToolMeta")
	}
	if s.Tool.Command != "rg" {
		t.Errorf("Command = %q, want rg", s.Tool.Command)
	}
	if len(s.Tool.Args) != 3 {
		t.Fatalf("len(Args) = %d, want 3", len(s.Tool.Args))
	}
	pat := s.Tool.Args["pattern"]
	if pat.Type != "string" || !pat.Required || pat.Position != 1 {
		t.Errorf("pattern arg = %+v", pat)
	}
	typ := s.Tool.Args["type"]
	if typ.Flag != "--type" {
		t.Errorf("type arg flag = %q, want --type", typ.Flag)
	}
}

func TestParsePromptSkill(t *testing.T) {
	content := `---
name: coder
description: A coding assistant
---
You are a helpful coding assistant.
`
	s, err := Parse(content, "coder.md")
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "coder" {
		t.Errorf("Name = %q, want coder", s.Name)
	}
	if s.Tool != nil {
		t.Errorf("Tool should be nil for prompt skill")
	}
	if s.Body != "You are a helpful coding assistant." {
		t.Errorf("Body = %q", s.Body)
	}
}

func TestParsePlainMarkdown(t *testing.T) {
	content := "# My Tool\n\nJust a plain markdown file."
	s, err := Parse(content, "mytool.md")
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "mytool" {
		t.Errorf("Name = %q, want mytool", s.Name)
	}
	if s.Tool != nil {
		t.Errorf("Tool should be nil for plain markdown")
	}
	if !s.IsTool() == true {
		// IsTool should be false
	}
	if s.IsTool() {
		t.Errorf("IsTool() = true, want false")
	}
}

func TestParseFileSkillMD(t *testing.T) {
	// SKILL.md naming convention: name comes from parent directory.
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "mygrep")
	os.MkdirAll(skillDir, 0755)
	path := filepath.Join(skillDir, "SKILL.md")
	os.WriteFile(path, []byte(`---
description: custom grep
metadata:
  command: grep
  args:
    pattern:
      type: string
      required: true
      position: 1
---
`), 0644)

	s, err := ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "mygrep" {
		t.Errorf("Name = %q, want mygrep (from parent dir)", s.Name)
	}
	if s.Tool == nil || s.Tool.Command != "grep" {
		t.Errorf("Tool = %+v, want command=grep", s.Tool)
	}
}

func TestParseDir(t *testing.T) {
	dir := t.TempDir()

	// Tool skill
	os.WriteFile(filepath.Join(dir, "bash.md"), []byte(`---
name: bash
description: Run shell commands
metadata:
  command: sh
  args:
    command:
      type: string
      required: true
      flag: "-c"
---
`), 0644)

	// Prompt skill
	os.WriteFile(filepath.Join(dir, "helper.md"), []byte("You are a helpful assistant."), 0644)

	// Non-md file (should be ignored)
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("not a skill"), 0644)

	skills, err := ParseDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 2 {
		t.Fatalf("len(skills) = %d, want 2", len(skills))
	}

	var tool, prompt *Skill
	for i := range skills {
		if skills[i].IsTool() {
			tool = &skills[i]
		} else {
			prompt = &skills[i]
		}
	}
	if tool == nil {
		t.Fatal("no tool skill found")
	}
	if tool.Name != "bash" {
		t.Errorf("tool.Name = %q, want bash", tool.Name)
	}
	if prompt == nil {
		t.Fatal("no prompt skill found")
	}
	if prompt.Name != "helper" {
		t.Errorf("prompt.Name = %q, want helper", prompt.Name)
	}
}

func TestParseSubcommands(t *testing.T) {
	content := `---
name: aga
description: LLM hub for agentic workflows
metadata:
  command: aga
  subcommands:
    ask:
      desc: Run a prompt
      args:
        prompt:
          type: string
          required: true
          position: 1
          desc: the prompt to send
        json:
          type: boolean
          flag: "--json"
          desc: return structured JSON output
    metrics:
      desc: Show usage summary
      args: {}
---
Use aga for LLM tasks.
`
	s, err := Parse(content, "aga.md")
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "aga" {
		t.Errorf("Name = %q, want aga", s.Name)
	}
	if s.Tool == nil {
		t.Fatal("Tool is nil")
	}
	if len(s.Tool.Subcommands) != 2 {
		t.Fatalf("len(Subcommands) = %d, want 2", len(s.Tool.Subcommands))
	}
	if !s.IsTool() {
		t.Error("IsTool() = false, want true")
	}

	// BuildArgv with subcommand=ask
	argv, err := s.BuildArgv(`{"subcommand": "ask", "prompt": "hello world", "json": true}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"aga", "ask", "--json", "hello world"}
	if !slicesEqual(argv, want) {
		t.Errorf("argv = %v, want %v", argv, want)
	}

	// BuildArgv with subcommand=metrics
	argv, err = s.BuildArgv(`{"subcommand": "metrics"}`)
	if err != nil {
		t.Fatal(err)
	}
	wantMetrics := []string{"aga", "metrics"}
	if !slicesEqual(argv, wantMetrics) {
		t.Errorf("argv = %v, want %v", argv, wantMetrics)
	}

	// Missing subcommand → error
	_, err = s.BuildArgv(`{"prompt": "hello"}`)
	if err == nil {
		t.Error("expected error for missing subcommand")
	}

	// Unknown subcommand → error
	_, err = s.BuildArgv(`{"subcommand": "nope"}`)
	if err == nil {
		t.Error("expected error for unknown subcommand")
	}
}

func TestParseDirWithSubcommands(t *testing.T) {
	dir := t.TempDir()

	// Skill with subcommands — stays as one skill
	os.WriteFile(filepath.Join(dir, "aga.md"), []byte(`---
name: aga
description: LLM hub
metadata:
  command: aga
  subcommands:
    ask:
      desc: Run a prompt
      args:
        prompt:
          type: string
          required: true
          position: 1
    metrics:
      desc: Show metrics
      args: {}
---
`), 0644)

	// Regular skill
	os.WriteFile(filepath.Join(dir, "helper.md"), []byte("Just a prompt."), 0644)

	skills, err := ParseDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Should be 2: aga (one tool with subcommands), helper
	if len(skills) != 2 {
		t.Fatalf("len(skills) = %d, want 2", len(skills))
	}

	names := map[string]bool{}
	for _, s := range skills {
		names[s.Name] = true
	}
	for _, want := range []string{"aga", "helper"} {
		if !names[want] {
			t.Errorf("missing skill %q, got %v", want, names)
		}
	}
}

func TestNameFromPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"skills/grep.md", "grep"},
		{"skills/grep/SKILL.md", "grep"},
		{"skills/grep/skill.md", "grep"},
		{"my-tool.md", "my-tool"},
	}
	for _, tt := range tests {
		got := nameFromPath(tt.path)
		if got != tt.want {
			t.Errorf("nameFromPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}
