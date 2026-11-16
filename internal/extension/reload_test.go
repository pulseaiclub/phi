package extension_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/extension/create"

	"github.com/pulseaiclub/phi/internal/extension/core"
)

// These tests build and run real PXB extension subprocesses, so they exercise
// the parts a hand-built fiber cannot: the handshake, hot swapping a live
// process, and what happens when a child dies on its own.

// aliveDirEnv makes the fixture drop a marker file per process and remove it on
// shutdown. A leftover marker after a test means a subprocess the host never
// closed — the exact failure mode a resurrected or orphaned fiber produces.
const aliveDirEnv = "PHI_EXT_TEST_ALIVE_DIR"

const fixtureTemplate = `package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"%s

	"github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/phi"
)

func main() {
	m := phi.New("hot", "0.0.1")
	if dir := os.Getenv("%s"); dir != "" {
		marker := filepath.Join(dir, strconv.Itoa(os.Getpid()))
		_ = os.WriteFile(marker, nil, 0o644)
		defer os.Remove(marker)
	}
	m.RegisterCommand("%s", ext.Command{
		Description: "%s",
		Handler:     func(string, *ext.Context) error { return nil },
	})
	m.RegisterTool(ext.Tool{
		Name: "tool-%s",
		Execute: func(context.Context, json.RawMessage) (ext.ToolResult, error) {
			return ext.ToolResult{}, nil
		},
	})
%s	_ = m.Run()
}
`

// fixtureSource returns the "hot" extension: it registers a command and a tool
// named after its build, marks its lifetime with a file while alive, and (when
// sleepExpr is set, a Go duration expression such as "300 * time.Millisecond")
// exits on its own.
func fixtureSource(command, sleepExpr string) string {
	var timeImport, dieBlock string
	if sleepExpr != "" {
		timeImport = "\n\t\"time\""
		dieBlock = "\tgo func() {\n\t\ttime.Sleep(" + sleepExpr + ")\n\t\tos.Exit(0)\n\t}()\n"
	}
	return fmt.Sprintf(fixtureTemplate, timeImport, aliveDirEnv, command, command, command, dieBlock)
}

// markers lists the process markers still present in the alive directory.
func markers(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

// startFixture builds and loads one extension, returning the runner and dirs.
func startFixture(t *testing.T, source string, options ...extension.RunnerOption) (*core.Runner, string, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a real extension binary")
	}
	root := t.TempDir()
	extDir := filepath.Join(root, "hot")
	aliveDir := t.TempDir()
	t.Setenv(aliveDirEnv, aliveDir)

	require.NoError(t, create.Materialize(t.Context(), extDir, "hot", "0.0.1", source))

	runner, warns, err := extension.Load(root, "", options...)
	require.NoError(t, err)
	require.Empty(t, warns, "%v", warns)
	t.Cleanup(runner.Close)
	return runner, extDir, aliveDir
}

func commandNames(r *core.Runner) []string {
	entries := r.CommandEntries()
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name
	}
	return out
}

func TestLoadAndReloadPreserveHostConstruction(t *testing.T) {
	var changes int
	runner, _, _ := startFixture(t, fixtureSource("v1", ""),
		extension.WithHost(ext.HostOpts{Cwd: "/initial", SessionID: "initial", HasUI: true}),
		extension.WithOnChange(func() { changes++ }))
	api := runner.Fiber("hot").API()
	assert.Equal(t, "/initial", api.NewContext().Cwd)
	assert.Equal(t, "initial", api.NewContext().SessionID)
	assert.True(t, api.NewContext().HasUI)
	assert.Len(t, api.GetAllTools(), 1)
	assert.Equal(t, 1, changes)

	runner.SetMeta("next", "/next")
	assert.Equal(t, "next", api.NewContext().SessionID)
	require.NoError(t, runner.ReloadExtension("hot"))
	api = runner.Fiber("hot").API()
	assert.Equal(t, "/next", api.NewContext().Cwd)
	assert.Equal(t, "next", api.NewContext().SessionID)
	assert.True(t, api.NewContext().HasUI)
	assert.Len(t, api.GetAllTools(), 1)
	assert.Equal(t, 2, changes)
}

func TestReloadSwapsRunningExtension(t *testing.T) {
	runner, extDir, aliveDir := startFixture(t, fixtureSource("v1", ""))
	require.Equal(t, []string{"v1"}, commandNames(runner))
	require.Len(t, markers(t, aliveDir), 1)

	// Rebuild the same directory and hot swap it.
	require.NoError(t, create.Materialize(
		t.Context(), extDir, "hot", "0.0.2", fixtureSource("v2", ""),
	))
	require.NoError(t, runner.ReloadExtension("hot"))

	// The cached read model must have been rebuilt for the new process.
	assert.Equal(t, []string{"v2"}, commandNames(runner))
	state, err := runner.GetExtensionState("hot")
	require.NoError(t, err)
	assert.Equal(t, core.FiberActive, state)

	// Exactly one live process: the replaced one was closed, not leaked.
	require.Eventually(t, func() bool {
		return len(markers(t, aliveDir)) == 1
	}, 5*time.Second, 10*time.Millisecond, "the replaced process must shut down")
}

func TestReloadFailureKeepsOldProcessServing(t *testing.T) {
	runner, extDir, aliveDir := startFixture(t, fixtureSource("v1", ""))
	require.Equal(t, []string{"v1"}, commandNames(runner))

	// A binary that exits instead of handshaking: the replacement cannot start.
	require.NoError(t, create.Materialize(
		t.Context(), extDir, "hot", "0.0.1", "package main\n\nfunc main() {}\n",
	))
	assert.ErrorContains(t, runner.ReloadExtension("hot"), "reload")

	// The old process is still serving, so the fiber must not claim failure.
	state, err := runner.GetExtensionState("hot")
	require.NoError(t, err)
	assert.Equal(t, core.FiberActive, state)
	assert.Equal(t, []string{"v1"}, commandNames(runner))

	// And a good build still swaps in afterwards.
	require.NoError(t, create.Materialize(
		t.Context(), extDir, "hot", "0.0.3", fixtureSource("v3", ""),
	))
	require.NoError(t, runner.ReloadExtension("hot"))
	assert.Equal(t, []string{"v3"}, commandNames(runner))
	require.Eventually(t, func() bool {
		return len(markers(t, aliveDir)) == 1
	}, 5*time.Second, 10*time.Millisecond)
}

func TestConcurrentReloadAndUnloadLeavesNoOrphan(t *testing.T) {
	runner, _, aliveDir := startFixture(t, fixtureSource("v1", ""))
	require.Len(t, markers(t, aliveDir), 1)

	// Reload and unload race on the same fiber. Every operation goes through the
	// fiber's own lock, so whichever order wins, the loser must not publish a
	// process nobody tracks.
	const rounds = 6
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range rounds {
		wg.Go(func() {
			<-start
			if i%2 == 0 {
				_ = runner.ReloadExtension("hot")
				return
			}
			_ = runner.UnloadExtension("hot")
		})
	}
	close(start)
	wg.Wait()
	runner.Close()

	// Nothing may survive: every process the host started is closed.
	require.Eventually(t, func() bool {
		return len(markers(t, aliveDir)) == 0
	}, 10*time.Second, 20*time.Millisecond, "leaked extension subprocess")

	// An unloaded extension stays unloaded.
	if runner.Fiber("hot") == nil {
		_, err := runner.GetExtensionState("hot")
		assert.Error(t, err)
		assert.NotContains(t, runner.ListExtensions(), "hot")
	}
}

func TestFiberReportsFailureWhenChildDies(t *testing.T) {
	runner, _, _ := startFixture(t, fixtureSource("v1", "300 * time.Millisecond"))

	state, err := runner.GetExtensionState("hot")
	require.NoError(t, err)
	require.Equal(t, core.FiberActive, state)

	// The child exits on its own: the host must stop advertising it.
	require.Eventually(t, func() bool {
		s, err := runner.GetExtensionState("hot")
		return err == nil && s == core.FiberFailed
	}, 5*time.Second, 20*time.Millisecond, "a dead process must not read as active")

	assert.Equal(t, core.FiberFailed, runner.ListExtensions()["hot"])
	assert.Error(t, runner.Fiber("hot").Error())
}
