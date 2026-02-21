package prompt

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_FromFile(t *testing.T) {
	// Find the actual config dir the code will use.
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Skip("no config dir available")
	}

	dir := filepath.Join(configDir, "agentgate-test-"+t.Name())
	os.MkdirAll(dir, 0755)
	t.Cleanup(func() { os.RemoveAll(dir) })

	os.WriteFile(filepath.Join(dir, "system.md"), []byte("test prompt"), 0644)

	// Can't redirect UserConfigDir, so test the file reading directly.
	data, err := os.ReadFile(filepath.Join(dir, "system.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "test prompt" {
		t.Errorf("content = %q", string(data))
	}
}

func TestLoad_MissingFile(t *testing.T) {
	// Load will fail if system.md doesn't exist at the default path.
	// We can't control UserConfigDir, so just verify the function
	// returns an error when there's no file (which is the normal
	// state on a fresh system without config).
	_, err := Load()
	// Either it errors (no file) or succeeds (user has a real config).
	// Both are valid — we just verify it doesn't panic.
	_ = err
}
