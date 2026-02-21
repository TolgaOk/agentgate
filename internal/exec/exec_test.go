package exec

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSimpleCommand(t *testing.T) {
	r, err := Execute(context.Background(), "echo hello", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", r.ExitCode)
	}
	if got := strings.TrimSpace(r.Stdout); got != "hello" {
		t.Errorf("Stdout = %q, want %q", got, "hello")
	}
}

func TestStderrCapture(t *testing.T) {
	r, err := Execute(context.Background(), "echo err >&2", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(r.Stderr); got != "err" {
		t.Errorf("Stderr = %q, want %q", got, "err")
	}
}

func TestExitCode(t *testing.T) {
	r, err := Execute(context.Background(), "exit 42", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode != 42 {
		t.Errorf("ExitCode = %d, want 42", r.ExitCode)
	}
}

func TestTimeout(t *testing.T) {
	start := time.Now()
	_, err := Execute(context.Background(), "sleep 60", 200*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Error("expected error from timeout")
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %v, expected fast return on timeout", elapsed)
	}
}

func TestCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	start := time.Now()
	_, err := Execute(ctx, "sleep 60", 30*time.Second)
	elapsed := time.Since(start)

	if err == nil {
		t.Error("expected error from cancelled context")
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %v, expected fast return on cancel", elapsed)
	}
}

func TestOutputTruncation(t *testing.T) {
	// Generate ~64KB of output (well over 32KB limit).
	r, err := Execute(context.Background(), "dd if=/dev/zero bs=1024 count=64 2>/dev/null | tr '\\0' 'A'", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Stdout, "truncated") {
		t.Error("expected truncation marker in stdout")
	}
	if len(r.Stdout) > maxOutputBytes+200 { // some slack for the truncation message
		t.Errorf("stdout length = %d, expected <= ~%d", len(r.Stdout), maxOutputBytes+200)
	}
}

func TestStdoutAndStderrSeparate(t *testing.T) {
	r, err := Execute(context.Background(), "echo out && echo err >&2", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(r.Stdout); got != "out" {
		t.Errorf("Stdout = %q, want %q", got, "out")
	}
	if got := strings.TrimSpace(r.Stderr); got != "err" {
		t.Errorf("Stderr = %q, want %q", got, "err")
	}
}
