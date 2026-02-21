package policy

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckAllowed(t *testing.T) {
	p := Default()
	d := p.Check("echo hello")
	if d.Kind != Allow {
		t.Errorf("Check(echo hello) = %v, want Allow", d.Kind)
	}
}

func TestCheckBlocked(t *testing.T) {
	p := Default()
	tests := []string{
		"rm -rf /",
		"sudo apt install foo",
		"echo test > /dev/sda",
	}
	for _, cmd := range tests {
		d := p.Check(cmd)
		if d.Kind != Block {
			t.Errorf("Check(%q) = %v, want Block", cmd, d.Kind)
		}
	}
}

func TestCheckConfirm(t *testing.T) {
	p := Default()
	tests := []string{
		"rm file.txt",
		"git push origin main",
	}
	for _, cmd := range tests {
		d := p.Check(cmd)
		if d.Kind != Confirm {
			t.Errorf("Check(%q) = %v, want Confirm", cmd, d.Kind)
		}
	}
}

func TestBlockedTakesPriority(t *testing.T) {
	p := Policy{
		Blocked: []string{"sudo "},
		Confirm: []string{"sudo "},
	}
	d := p.Check("sudo rm -rf /")
	if d.Kind != Block {
		t.Errorf("Check(sudo rm -rf /) = %v, want Block (blocked > confirm)", d.Kind)
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.toml")
	os.WriteFile(path, []byte(`
timeout = "10s"
blocked = ["dangerous"]
confirm = ["careful"]
`), 0644)

	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Timeout != 10*time.Second {
		t.Errorf("Timeout = %v, want 10s", p.Timeout)
	}
	if len(p.Blocked) != 1 || p.Blocked[0] != "dangerous" {
		t.Errorf("Blocked = %v, want [dangerous]", p.Blocked)
	}
	if len(p.Confirm) != 1 || p.Confirm[0] != "careful" {
		t.Errorf("Confirm = %v, want [careful]", p.Confirm)
	}
}

func TestLoadDefaultsApplied(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.toml")
	// Empty file — all defaults.
	os.WriteFile(path, []byte(``), 0644)

	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s (default)", p.Timeout)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/policy.toml")
	if err == nil {
		t.Error("Load() should fail for missing file")
	}
}
