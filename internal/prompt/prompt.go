package prompt

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/TolgaOk/agentgate/internal/config"
)

// Load returns the system prompt from ~/.config/agentgate/system.md.
func Load() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", fmt.Errorf("prompt: %w", err)
	}

	path := filepath.Join(dir, "system.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("prompt: %s not found — create it with your system prompt", path)
	}

	return string(data), nil
}
