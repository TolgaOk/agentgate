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
