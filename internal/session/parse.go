package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/TolgaOk/agentgate/internal/provider"
)

// Header is the first line of a session JSONL file.
type Header struct {
	Model string `json:"model"`
}

// Parse reads a session JSONL file. The first line is the header,
// subsequent lines are messages.
func Parse(r io.Reader) (Header, []provider.Message, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB line buffer

	// First line: header.
	var header Header
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return header, nil, fmt.Errorf("session: read header: %w", err)
		}
		return header, nil, nil // empty file
	}
	if err := json.Unmarshal(scanner.Bytes(), &header); err != nil {
		return header, nil, fmt.Errorf("session: parse header: %w", err)
	}

	// Remaining lines: messages.
	var messages []provider.Message
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg provider.Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			return header, messages, fmt.Errorf("session: parse message: %w", err)
		}
		messages = append(messages, msg)
	}

	if err := scanner.Err(); err != nil {
		return header, messages, fmt.Errorf("session: read: %w", err)
	}

	return header, messages, nil
}
