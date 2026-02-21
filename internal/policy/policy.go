package policy

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type DecisionKind int

const (
	Allow DecisionKind = iota
	Block
	Confirm
)

type Decision struct {
	Kind   DecisionKind
	Reason string // why blocked or what needs confirmation
}

type Policy struct {
	Timeout time.Duration
	Blocked []string
	Confirm []string
}

type policyFile struct {
	Timeout  string   `toml:"timeout"`
	Blocked  []string `toml:"blocked"`
	Confirm_ []string `toml:"confirm"`
}

// Default returns a policy with sensible defaults.
func Default() Policy {
	return Policy{
		Timeout: 30 * time.Second,
		Blocked: []string{"rm -rf /", "sudo ", "> /dev/"},
		Confirm: []string{"rm ", "git push"},
	}
}

// Load reads a policy from a TOML file.
func Load(path string) (Policy, error) {
	var pf policyFile
	if _, err := toml.DecodeFile(path, &pf); err != nil {
		return Policy{}, fmt.Errorf("policy: %w", err)
	}
	p := Default()
	if pf.Timeout != "" {
		d, err := time.ParseDuration(pf.Timeout)
		if err != nil {
			return Policy{}, fmt.Errorf("policy: invalid timeout: %w", err)
		}
		p.Timeout = d
	}
	if pf.Blocked != nil {
		p.Blocked = pf.Blocked
	}
	if pf.Confirm_ != nil {
		p.Confirm = pf.Confirm_
	}
	return p, nil
}

// LoadDefault loads policy from the standard location.
// Returns defaults if the file doesn't exist.
func LoadDefault() (Policy, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return Default(), nil
	}
	path := dir + "/agentgate/policy.toml"
	if _, err := os.Stat(path); err != nil {
		return Default(), nil
	}
	return Load(path)
}

// Check evaluates a command against the policy.
// Blocked patterns are checked first, then confirm patterns.
func (p Policy) Check(command string) Decision {
	for _, pat := range p.Blocked {
		if strings.Contains(command, pat) {
			return Decision{Kind: Block, Reason: fmt.Sprintf("matches blocked pattern %q", pat)}
		}
	}
	for _, pat := range p.Confirm {
		if strings.Contains(command, pat) {
			return Decision{Kind: Confirm, Reason: command}
		}
	}
	return Decision{Kind: Allow}
}
