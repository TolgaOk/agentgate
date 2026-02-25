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
	if os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "\033[33mwarning: no system prompt at %s\033[0m\n", path)
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("prompt: %w", err)
	}

	return string(data), nil
}
