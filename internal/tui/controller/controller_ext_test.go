package controller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pulseaiclub/phi/internal/extension/create"
	"github.com/pulseaiclub/phi/internal/extension/loader"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/project"
)

func TestSwapExtensionRunner_ClosesPrevious(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PHI_MODEL", "test-model")
	t.Setenv("PHI_API_KEY", "test-key")
	t.Setenv("PHI_BASE_URL", "http://127.0.0.1:9")
	t.Setenv(loader.EnvExtensions, "off")

	cwd := t.TempDir()
	proj, err := project.Discover(cwd)
	require.NoError(t, err)
	require.NoError(t, proj.LoadConfig())

	ctrl, err := NewController(NewBus(nil), proj)
	require.NoError(t, err)

	first, _, err := extension.Load(t.TempDir(), "")
	require.NoError(t, err)
	require.NotNil(t, first)
	ctrl.bus.Drain() // drop startup messages
	ctrl.swapExtensionRunner(first)
	assert.Same(t, first, ctrl.Extensions())
	assertStatusReset(t, ctrl.bus)

	second, _, err := extension.Load(t.TempDir(), "")
	require.NoError(t, err)
	require.NotNil(t, second)
	ctrl.bus.Drain()
	ctrl.swapExtensionRunner(second)
	assert.Same(t, second, ctrl.Extensions())
	assertStatusReset(t, ctrl.bus)
	// Previous runner must be closed; a second Close is a safe no-op.
	first.Close()

	ctrl.Close()
	assert.Nil(t, ctrl.Extensions())
	// Idempotent.
	ctrl.Close()
}

func TestResumeExtensionLifecycleUsesOutgoingRunner(t *testing.T) {
	t.Setenv("PHI_MODEL", "test-model")
	t.Setenv("PHI_API_KEY", "test-key")
	t.Setenv("PHI_BASE_URL", "http://127.0.0.1:9")

	t.Setenv(loader.EnvExtensions, "")
	t.Setenv("PHI_THINK_LEVEL", "")
	logPath := filepath.Join(t.TempDir(), "lifecycle.log")
	t.Setenv("PHI_TEST_LIFECYCLE_LOG", logPath)
	cwd := t.TempDir()
	extDir := filepath.Join(cwd, ".phi", "extensions", "lifecycle")
	src := `package main

import (
	"fmt"
	"os"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/phi"
)

func record(kind string) {
	f, err := os.OpenFile(os.Getenv("PHI_TEST_LIFECYCLE_LOG"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(f, "%s %d\n", kind, os.Getpid())
	_ = f.Close()
}

func main() {
	m := phi.New("lifecycle", "0.0.1")
	m.Subscribe(ext.EventSessionStart, func() { record("start") })
	m.Subscribe(ext.EventSessionShutdown, func() { record("shutdown") })
	_ = m.Run()
}
`
	require.NoError(t, create.Materialize(t.Context(), extDir, "lifecycle", "0.0.1", src))
	// Build before isolating HOME so Go can use its existing module cache.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	proj, err := project.Discover(cwd)
	require.NoError(t, err)
	ctrl, err := NewController(NewBus(nil), proj)
	require.NoError(t, err)
	t.Cleanup(ctrl.Close)

	events := func() map[string]map[string]bool {
		data, err := os.ReadFile(logPath)
		if err != nil {
			return nil
		}
		out := make(map[string]map[string]bool)
		for line := range strings.SplitSeq(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			pid := fields[1]
			if _, ok := out[pid]; !ok {
				out[pid] = make(map[string]bool)
			}
			out[pid][fields[0]] = true
		}
		return out
	}
	var oldPID string
	require.Eventually(t, func() bool {
		for pid, got := range events() {
			if got["start"] {
				oldPID = pid
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond)

	target, err := ctrl.engine.Session().Fork()
	require.NoError(t, err)
	_, err = ctrl.Resume(target.ID())
	require.NoError(t, err)

	var incomingPID string
	require.Eventually(t, func() bool {
		got := events()
		for pid, eventSet := range got {
			if pid != oldPID && eventSet["start"] {
				incomingPID = pid
			}
		}
		return incomingPID != "" && got[oldPID]["shutdown"]
	}, 5*time.Second, 10*time.Millisecond)

	got := events()
	assert.True(t, got[oldPID]["shutdown"], "outgoing extension must receive session_shutdown")
	assert.False(t, got[incomingPID]["shutdown"], "incoming extension must not receive outgoing session_shutdown")
}

// assertStatusReset requires a footer extension-status reset message: swapping
// the runner kills the previous subprocesses, whose UI state must not linger.
func assertStatusReset(t *testing.T, bus *Bus) {
	t.Helper()
	for _, m := range bus.Drain() {
		if eff, ok := m.(ExtSessionEffectsMsg); ok && eff.StatusSet && eff.Status == "" {
			return
		}
	}
	require.Fail(t, "expected an extension status reset (ExtSessionEffectsMsg{StatusSet:true, Status:\"\"})")
}
