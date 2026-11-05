//go:build windows

package bashtool

import (
	"context"
	"io"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// processGroupAttr hides the console window of spawned shells.
// Windows has no POSIX signals; trees are killed via taskkill.
func processGroupAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true}
}

// killProcessTree terminates pid and its descendants via taskkill /T.
// Errors are ignored (best-effort): the process may already be gone,
// and Cmd.WaitDelay hard-kills the shell if the tree survives.
func killProcessTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	// Cmd.WaitDelay starts only after Cancel returns, so taskkill needs its
	// own deadline to keep a stalled tree lookup from blocking cancellation.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	//nolint:gosec // G204: fixed executable and flags; the only argument is an integer PID.
	kill := exec.CommandContext(ctx, "taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	kill.Stdout = io.Discard
	kill.Stderr = io.Discard
	_ = kill.Run()
	return nil
}
