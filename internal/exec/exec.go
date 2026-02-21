package exec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

const maxOutputBytes = 32 * 1024 // 32KB

type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Execute runs a shell command with the given timeout.
// It creates a process group so the entire tree can be killed on timeout.
func Execute(ctx context.Context, command string, timeout time.Duration) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// Kill the process group if context was cancelled/timed out.
	if ctx.Err() != nil && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	r := Result{
		Stdout: truncate(stdout.Bytes()),
		Stderr: truncate(stderr.Bytes()),
	}

	if err != nil {
		// Context cancellation/timeout takes priority — the process
		// was killed by us, not by its own logic.
		if ctx.Err() != nil {
			return r, fmt.Errorf("exec: %w", ctx.Err())
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			r.ExitCode = exitErr.ExitCode()
		} else {
			return r, fmt.Errorf("exec: %w", err)
		}
	}

	return r, nil
}

func truncate(b []byte) string {
	if len(b) <= maxOutputBytes {
		return string(b)
	}
	return string(b[:maxOutputBytes]) + fmt.Sprintf("\n... (truncated, %d bytes total)", len(b))
}
