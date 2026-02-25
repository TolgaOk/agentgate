package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agexec "github.com/TolgaOk/agentgate/internal/exec"
)

func TestE2E_GrepSkill(t *testing.T) {
	// Create a temp skill file.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "grep.md"), []byte(`---
name: grep
description: Search file contents using ripgrep
metadata:
  command: grep
  args:
    pattern:
      type: string
      required: true
      position: 1
      desc: regex pattern
    path:
      type: string
      position: 2
---
`), 0644)

	// Parse the skill directory.
	skills, err := ParseDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("len(skills) = %d, want 1", len(skills))
	}

	s := skills[0]
	if !s.IsTool() {
		t.Fatal("expected tool skill")
	}

	// Verify ToToolDef produces valid schema.
	td := s.ToToolDef()
	if td.Name != "grep" {
		t.Errorf("ToolDef.Name = %q", td.Name)
	}

	// Simulate LLM returning JSON params.
	argv, err := s.BuildArgv(`{"pattern": "hello", "path": "."}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"grep", "hello", "."}
	if !slicesEqual(argv, want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}

	// Execute via ExecuteDirect against a temp file.
	target := filepath.Join(dir, "testfile.txt")
	os.WriteFile(target, []byte("hello world\ngoodbye world\n"), 0644)

	argv, err = s.BuildArgv(`{"pattern": "hello", "path": "` + target + `"}`)
	if err != nil {
		t.Fatal(err)
	}

	result, err := agexec.ExecuteDirect(context.Background(), argv, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Stdout, "hello world") {
		t.Errorf("stdout = %q, want to contain 'hello world'", result.Stdout)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}
}

func TestE2E_EchoSkill(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "echo.md"), []byte(`---
name: echo
description: Print a message
metadata:
  command: echo
  args:
    message:
      type: string
      required: true
      position: 1
---
`), 0644)

	skills, err := ParseDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := skills[0]

	argv, err := s.BuildArgv(`{"message": "skill system works"}`)
	if err != nil {
		t.Fatal(err)
	}

	result, err := agexec.ExecuteDirect(context.Background(), argv, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result.Stdout) != "skill system works" {
		t.Errorf("stdout = %q, want 'skill system works'", result.Stdout)
	}
}

func TestE2E_PromptSkillHasNoTool(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "helper.md"), []byte("You are a helpful coding assistant."), 0644)

	skills, err := ParseDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("len = %d", len(skills))
	}
	if skills[0].IsTool() {
		t.Error("prompt skill should not be a tool")
	}
	if skills[0].Body != "You are a helpful coding assistant." {
		t.Errorf("body = %q", skills[0].Body)
	}
}

func TestE2E_MixedSkillDir(t *testing.T) {
	dir := t.TempDir()

	// Tool skill.
	os.WriteFile(filepath.Join(dir, "ls.md"), []byte(`---
name: ls
description: List directory contents
metadata:
  command: ls
  args:
    path:
      type: string
      position: 1
    all:
      type: boolean
      flag: "-a"
---
`), 0644)

	// Prompt skill.
	os.WriteFile(filepath.Join(dir, "rules.md"), []byte("Always explain your reasoning."), 0644)

	skills, err := ParseDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 2 {
		t.Fatalf("len = %d, want 2", len(skills))
	}

	var toolCount, promptCount int
	for _, s := range skills {
		if s.IsTool() {
			toolCount++

			// Test execution with boolean flag.
			argv, err := s.BuildArgv(`{"path": "/tmp", "all": true}`)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"ls", "-a", "/tmp"}
			if !slicesEqual(argv, want) {
				t.Errorf("argv = %v, want %v", argv, want)
			}
		} else {
			promptCount++
		}
	}
	if toolCount != 1 || promptCount != 1 {
		t.Errorf("tools=%d prompts=%d, want 1 each", toolCount, promptCount)
	}
}
