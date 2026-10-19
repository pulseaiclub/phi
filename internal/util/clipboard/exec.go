package clipboard

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

const (
	defaultListTimeout = time.Second
	defaultReadTimeout = 3 * time.Second
	defaultMaxBytes    = 50 << 20 // clipboard read cap before image.MaxBytes trim
)

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("clipboard: %s: %w", name, err)
	}
	out := stdout.Bytes()
	if len(out) > defaultMaxBytes {
		return nil, fmt.Errorf("clipboard: %s: output too large (%d bytes)", name, len(out))
	}
	return out, nil
}

//nolint:unparam // linux/WSL probes pass defaultListTimeout for list reads, 5s for PowerShell
func runCommandTimeout(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return runCommand(ctx, name, args...)
}
