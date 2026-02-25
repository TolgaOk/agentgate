package policy

import (
	"testing"
)

func TestCheckAllowed(t *testing.T) {
	p := Policy{Allowed: []string{"bash", "grep", "ls"}}
	d := p.Check("bash")
	if d.Kind != Allow {
		t.Errorf("Check(bash) = %v, want Allow", d.Kind)
	}
}

func TestCheckBlocked(t *testing.T) {
	p := Policy{Blocked: []string{"dangerous"}}
	d := p.Check("dangerous")
	if d.Kind != Block {
		t.Errorf("Check(dangerous) = %v, want Block", d.Kind)
	}
}

func TestCheckConfirmByDefault(t *testing.T) {
	p := Policy{Allowed: []string{"bash"}}
	d := p.Check("unknown")
	if d.Kind != Confirm {
		t.Errorf("Check(unknown) = %v, want Confirm", d.Kind)
	}
}

func TestBlockedOverridesAllowed(t *testing.T) {
	p := Policy{
		Allowed: []string{"bash"},
		Blocked: []string{"bash"},
	}
	d := p.Check("bash")
	if d.Kind != Block {
		t.Errorf("Check(bash) = %v, want Block (blocked > allowed)", d.Kind)
	}
}

func TestCheckExactMatch(t *testing.T) {
	p := Policy{Allowed: []string{"ls"}}

	// Exact match → Allow.
	d := p.Check("ls")
	if d.Kind != Allow {
		t.Errorf("Check(ls) = %v, want Allow", d.Kind)
	}

	// Similar name → Confirm (no substring/prefix matching).
	d = p.Check("lsof")
	if d.Kind != Confirm {
		t.Errorf("Check(lsof) = %v, want Confirm (exact match only)", d.Kind)
	}
}

func TestCheckBlockedExactMatch(t *testing.T) {
	p := Policy{Blocked: []string{"rm"}}

	d := p.Check("rm")
	if d.Kind != Block {
		t.Errorf("Check(rm) = %v, want Block", d.Kind)
	}

	// "remark" is not "rm".
	d = p.Check("remark")
	if d.Kind != Confirm {
		t.Errorf("Check(remark) = %v, want Confirm (exact match only)", d.Kind)
	}
}

func TestCheckEmptyPolicy(t *testing.T) {
	p := Policy{}
	d := p.Check("anything")
	if d.Kind != Confirm {
		t.Errorf("Check with empty policy = %v, want Confirm", d.Kind)
	}
}

func TestCheckMultipleAllowed(t *testing.T) {
	p := Policy{Allowed: []string{"bash", "grep", "cat", "ls"}}
	tests := []struct {
		tool string
		want DecisionKind
	}{
		{"bash", Allow},
		{"grep", Allow},
		{"cat", Allow},
		{"ls", Allow},
		{"rm", Confirm},
		{"curl", Confirm},
	}
	for _, tt := range tests {
		d := p.Check(tt.tool)
		if d.Kind != tt.want {
			t.Errorf("Check(%q) = %v, want %v", tt.tool, d.Kind, tt.want)
		}
	}
}

func TestCheckMultipleBlocked(t *testing.T) {
	p := Policy{Blocked: []string{"sudo", "rm", "eval"}}
	tests := []struct {
		tool string
		want DecisionKind
	}{
		{"sudo", Block},
		{"rm", Block},
		{"eval", Block},
		{"bash", Confirm},
		{"ls", Confirm},
	}
	for _, tt := range tests {
		d := p.Check(tt.tool)
		if d.Kind != tt.want {
			t.Errorf("Check(%q) = %v, want %v", tt.tool, d.Kind, tt.want)
		}
	}
}
