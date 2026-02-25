package policy

import (
	"fmt"
	"time"
)

type DecisionKind int

const (
	Allow DecisionKind = iota
	Block
	Confirm
)

type Decision struct {
	Kind   DecisionKind
	Reason string
}

type Policy struct {
	Timeout time.Duration
	Allowed []string
	Blocked []string
}

// Default returns a policy with sensible defaults.
func Default() Policy {
	return Policy{
		Timeout: 30 * time.Second,
		Allowed: []string{},
		Blocked: []string{},
	}
}

// Check evaluates a tool name against the policy.
// Order: blocked → allowed → confirm (default).
func (p Policy) Check(toolName string) Decision {
	for _, name := range p.Blocked {
		if toolName == name {
			return Decision{Kind: Block, Reason: fmt.Sprintf("tool %q is blocked", toolName)}
		}
	}
	for _, name := range p.Allowed {
		if toolName == name {
			return Decision{Kind: Allow}
		}
	}
	return Decision{Kind: Confirm, Reason: toolName}
}
