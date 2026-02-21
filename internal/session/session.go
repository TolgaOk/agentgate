package session

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/TolgaOk/agentgate/internal/agent"
	"github.com/TolgaOk/agentgate/internal/provider"
)

// Session is an interactive multi-turn conversation persisted to a JSONL file.
type Session struct {
	ID       string
	FilePath string
	Model    string
	Messages []provider.Message
	file     *os.File // append-only handle
}

// New creates a new session file in dir with a timestamped ID.
func New(dir, model string) (*Session, error) {
	now := time.Now().UTC()
	id := now.Format("2006-01-02_15-04-05")
	path := filepath.Join(dir, id+".jsonl")

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("session: mkdir: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("session: create: %w", err)
	}

	header := Header{Model: model}
	if err := writeJSONLine(f, header); err != nil {
		f.Close()
		return nil, fmt.Errorf("session: write header: %w", err)
	}

	return &Session{
		ID:       id,
		FilePath: path,
		Model:    model,
		file:     f,
	}, nil
}

// Open parses an existing session file and reopens it for appending.
func Open(path string) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("session: read: %w", err)
	}

	header, msgs, err := Parse(f)
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("session: parse: %w", err)
	}

	af, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("session: reopen: %w", err)
	}

	base := filepath.Base(path)
	id := base[:len(base)-len(filepath.Ext(base))]

	return &Session{
		ID:       id,
		FilePath: path,
		Model:    header.Model,
		Messages: msgs,
		file:     af,
	}, nil
}

// AppendMessage writes a single message as a JSONL line and adds it to Messages.
func (s *Session) AppendMessage(msg provider.Message) error {
	if err := writeJSONLine(s.file, msg); err != nil {
		return fmt.Errorf("session: write: %w", err)
	}
	s.Messages = append(s.Messages, msg)
	return nil
}

// AppendMessages writes multiple messages to the file.
func (s *Session) AppendMessages(msgs []provider.Message) error {
	for _, msg := range msgs {
		if err := s.AppendMessage(msg); err != nil {
			return err
		}
	}
	return nil
}

// Run starts an interactive session loop, reading user input from in and
// sending agent responses to out. It persists each turn to the session file.
func (s *Session) Run(ctx context.Context, a *agent.Agent, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)

	if len(s.Messages) > 0 {
		fmt.Fprintf(out, "Resumed session (%d messages)\n", len(s.Messages))
	}

	for {
		fmt.Fprint(out, "\n> ")
		if !scanner.Scan() {
			return scanner.Err()
		}
		line := scanner.Text()

		if line == "/quit" || line == "/exit" {
			return nil
		}
		if line == "" {
			continue
		}

		userMsg := provider.Message{
			Role:    provider.RoleUser,
			Content: line,
			Meta: map[string]string{
				"date": time.Now().UTC().Format(time.RFC3339),
			},
		}
		if err := s.AppendMessage(userMsg); err != nil {
			return err
		}

		prevLen := len(s.Messages)
		_, usage, allMsgs, err := a.RunMessages(ctx, s.Messages)
		if err != nil {
			return err
		}

		newMsgs := allMsgs[prevLen:]
		if err := s.AppendMessages(newMsgs); err != nil {
			return err
		}

		fmt.Fprintf(out, "\n[tokens: %d in / %d out]\n", usage.InputTokens, usage.OutputTokens)
	}
}

// Close closes the underlying file.
func (s *Session) Close() error {
	if s.file != nil {
		return s.file.Close()
	}
	return nil
}

func writeJSONLine(f *os.File, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = f.Write(data)
	return err
}
