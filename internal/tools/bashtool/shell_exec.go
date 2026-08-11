package bashtool

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ShellExecResult is the outcome of a streaming shell run.
type ShellExecResult struct {
	Output   string
	ExitCode int
	Canceled bool
}

// ShellExecOptions configures interactive / streaming shell execution.
type ShellExecOptions struct {
	OnChunk func(chunk string)
}

// shellOutputWriter combines stdout and stderr while preserving the streaming
// callback. Using it as Cmd.Stdout and Cmd.Stderr lets os/exec own the copy
// machinery, so Cmd.Run waits for all output before closing the pipes.
// Collection is bounded independently of streaming callbacks, whose consumers
// are responsible for applying their own retention limits.
type shellOutputWriter struct {
	cb      *cappedBuffer
	onChunk func(chunk string)
}

func (w *shellOutputWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if _, err := w.cb.Write(p); err != nil {
		return 0, err
	}
	if w.onChunk != nil {
		w.onChunk(string(p))
	}
	return len(p), nil
}

// Collected returns the retained command output without display metadata.
func (w *shellOutputWriter) Collected() string {
	return w.cb.String()
}

// ExecShell runs command via bash -c, streaming combined stdout+stderr.
func ExecShell(ctx context.Context, command string, opts ShellExecOptions) (ShellExecResult, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return ShellExecResult{}, fmt.Errorf("empty command")
	}

	cmd, err := buildShellCommand(ctx, command)
	if err != nil {
		return ShellExecResult{}, err
	}
	output := &shellOutputWriter{cb: newCappedBuffer(BashMaxCollectBytes), onChunk: opts.OnChunk}
	cmd.Stdout = output
	cmd.Stderr = output
	waitErr := cmd.Run()

	out := formatBashOutput(output.Collected(), output.cb.Truncated())

	res := ShellExecResult{Output: out}
	if errors.Is(ctx.Err(), context.Canceled) {
		res.Canceled = true
		return res, nil
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res, nil
		}
		return res, waitErr
	}
	return res, nil
}
