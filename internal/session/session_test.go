package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/TolgaOk/agentgate/internal/provider"
)

func TestNew_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, "test-model")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	if s.ID == "" {
		t.Error("ID is empty")
	}
	if s.Model != "test-model" {
		t.Errorf("Model = %q", s.Model)
	}
	if _, err := os.Stat(s.FilePath); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestNew_HeaderContent(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, "anthropic/claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Close()

	f, err := os.Open(s.FilePath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	header, msgs, err := Parse(f)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if header.Model != "anthropic/claude-sonnet-4-6" {
		t.Errorf("Model = %q", header.Model)
	}
	if len(msgs) != 0 {
		t.Errorf("got %d messages, want 0", len(msgs))
	}
}

func TestAppendMessage(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	s.AppendMessage(provider.Message{Role: provider.RoleUser, Content: "hello"})
	s.AppendMessage(provider.Message{Role: provider.RoleAssistant, Content: "hi there"})
	s.Close()

	if len(s.Messages) != 2 {
		t.Fatalf("in-memory messages = %d, want 2", len(s.Messages))
	}
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, "round-trip-model")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	s.AppendMessage(provider.Message{Role: provider.RoleUser, Content: "first question"})
	s.AppendMessage(provider.Message{Role: provider.RoleAssistant, Content: "first answer"})
	s.AppendMessage(provider.Message{Role: provider.RoleUser, Content: "second question"})
	s.AppendMessage(provider.Message{Role: provider.RoleAssistant, Content: "second answer"})
	s.Close()

	s2, err := Open(s.FilePath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s2.Close()

	if s2.ID != s.ID {
		t.Errorf("ID = %q, want %q", s2.ID, s.ID)
	}
	if s2.Model != "round-trip-model" {
		t.Errorf("Model = %q", s2.Model)
	}
	if len(s2.Messages) != 4 {
		t.Fatalf("messages = %d, want 4", len(s2.Messages))
	}

	expects := []struct {
		role    provider.Role
		content string
	}{
		{provider.RoleUser, "first question"},
		{provider.RoleAssistant, "first answer"},
		{provider.RoleUser, "second question"},
		{provider.RoleAssistant, "second answer"},
	}
	for i, e := range expects {
		if s2.Messages[i].Role != e.role || s2.Messages[i].Content != e.content {
			t.Errorf("msg[%d] = {%s, %q}, want {%s, %q}",
				i, s2.Messages[i].Role, s2.Messages[i].Content, e.role, e.content)
		}
	}
}

func TestRoundTrip_WithToolCalls(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	s.AppendMessage(provider.Message{Role: provider.RoleUser, Content: "list files"})
	s.AppendMessage(provider.Message{
		Role:    provider.RoleAssistant,
		Content: "Let me check.",
		ToolCalls: []provider.ToolCall{
			{ID: "tc_1", Name: "bash", Input: "ls -la"},
		},
		Meta: map[string]string{"model": "test", "input_tokens": "100"},
	})
	s.AppendMessage(provider.Message{
		Role: provider.RoleUser,
		ToolResult: &provider.ToolResult{
			ToolCallID: "tc_1",
			Content:    "file1.txt\nfile2.txt",
		},
		Meta: map[string]string{"duration": "50ms"},
	})
	s.AppendMessage(provider.Message{
		Role:    provider.RoleAssistant,
		Content: "Found 2 files.",
	})
	s.Close()

	s2, err := Open(s.FilePath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s2.Close()

	if len(s2.Messages) != 4 {
		t.Fatalf("messages = %d, want 4", len(s2.Messages))
	}

	// Check tool call.
	if len(s2.Messages[1].ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(s2.Messages[1].ToolCalls))
	}
	tc := s2.Messages[1].ToolCalls[0]
	if tc.ID != "tc_1" || tc.Name != "bash" || tc.Input != "ls -la" {
		t.Errorf("tool call = %+v", tc)
	}

	// Check tool result.
	tr := s2.Messages[2].ToolResult
	if tr == nil {
		t.Fatal("tool result is nil")
	}
	if tr.ToolCallID != "tc_1" || tr.Content != "file1.txt\nfile2.txt" {
		t.Errorf("tool result = %+v", tr)
	}
}

func TestOpen_AppendMore(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.AppendMessage(provider.Message{Role: provider.RoleUser, Content: "turn 1"})
	s.AppendMessage(provider.Message{Role: provider.RoleAssistant, Content: "reply 1"})
	s.Close()

	s2, err := Open(s.FilePath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s2.AppendMessage(provider.Message{Role: provider.RoleUser, Content: "turn 2"})
	s2.AppendMessage(provider.Message{Role: provider.RoleAssistant, Content: "reply 2"})
	s2.Close()

	// Verify by re-opening.
	s3, err := Open(s.FilePath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s3.Close()
	if len(s3.Messages) != 4 {
		t.Fatalf("messages = %d, want 4", len(s3.Messages))
	}
}

func TestNewAt_CreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "my-session.jsonl")
	s, err := NewAt(path, "test-model")
	if err != nil {
		t.Fatalf("NewAt: %v", err)
	}
	defer s.Close()

	if s.FilePath != path {
		t.Errorf("FilePath = %q, want %q", s.FilePath, path)
	}
	if s.ID != "my-session" {
		t.Errorf("ID = %q, want %q", s.ID, "my-session")
	}
	if s.Model != "test-model" {
		t.Errorf("Model = %q", s.Model)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}

	// Verify round-trip.
	s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s2.Close()
	if s2.Model != "test-model" {
		t.Errorf("reopened Model = %q", s2.Model)
	}
}

func TestNew_CreatesSubdirs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b", "c")
	s, err := New(dir, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Close()

	if _, err := os.Stat(s.FilePath); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}
