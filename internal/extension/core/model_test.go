package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ext "github.com/pulseaiclub/phi/ext/go"

	"github.com/pulseaiclub/phi/internal/extension/loader"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

// handBuiltFiber returns a fiber whose API is populated by hand.
//
// Registration is write-once before Ready — the SDK serializes tools, commands
// and subscriptions during the handshake and Proc drops register frames
// afterwards — so a hand-built API is indistinguishable from a handshaken one
// for the read paths. It has no process, so lifecycle assertions (active,
// failed, disposed) belong in the fiber and lifecycle tests instead.
func handBuiltFiber(name string) (*Fiber, *ext.API) {
	api := ext.NewAPI()
	f := NewFiber(name, loader.Discovered{Manifest: loader.Manifest{Name: name}})
	f.Attach(api, FiberActive)
	return f, api
}

func noopHandler(ext.ToolCallEvent, *ext.Context) *ext.ToolCallResult { return nil }

func noopTool(name string) ext.Tool {
	return ext.Tool{
		Name:    name,
		Execute: func(context.Context, json.RawMessage) (ext.ToolResult, error) { return ext.ToolResult{}, nil },
	}
}

func noopCommand() ext.Command {
	return ext.Command{Handler: func(string, *ext.Context) error { return nil }}
}

func TestModelAggregatesInNameOrder(t *testing.T) {
	r := NewRunner("")
	// Deliberately out of order: the model must not inherit map order.
	for _, name := range []string{"zeta", "alpha", "mid"} {
		f, api := handBuiltFiber(name)
		api.On(ext.EventToolCall, noopHandler)
		api.RegisterTool(noopTool("tool-" + name))
		r.mu.Lock()
		r.fibers[name] = f
		r.mu.Unlock()
	}

	m := r.snapshot()
	assert.Equal(t, []string{"alpha", "mid", "zeta"}, m.names)
	require.Len(t, m.handlers[ext.EventToolCall], 3)
	require.Len(t, m.extTools, 3)
	require.Len(t, m.tools, 3)
	for i, info := range m.tools {
		assert.Equal(t, []string{"tool-alpha", "tool-mid", "tool-zeta"}[i], info.Name)
		assert.Equal(t, "extension", info.Source)
	}
	assert.Empty(t, m.handlers["not_dispatched"])
}

func TestModelCacheIsInvalidatedOnTopologyChange(t *testing.T) {
	r := NewRunner("")
	f, api := handBuiltFiber("one")
	api.On(ext.EventToolResult, func(ext.ToolResultEvent, *ext.Context) *ext.ToolResultResult { return nil })
	r.mu.Lock()
	r.fibers["one"] = f
	r.mu.Unlock()

	first := r.snapshot()
	assert.Same(t, first, r.snapshot(), "the cached model is reused")
	require.Len(t, first.handlers[ext.EventToolResult], 1)

	second, secondAPI := handBuiltFiber("two")
	secondAPI.On(ext.EventToolResult, func(ext.ToolResultEvent, *ext.Context) *ext.ToolResultResult { return nil })
	r.mu.Lock()
	r.fibers["two"] = second
	r.invalidateLocked()
	r.mu.Unlock()

	fresh := r.snapshot()
	assert.NotSame(t, first, fresh)
	assert.Len(t, fresh.handlers[ext.EventToolResult], 2)
}

func TestModelCommandsFirstExtensionWins(t *testing.T) {
	r := NewRunner("")
	late, lateAPI := handBuiltFiber("zed")
	cmd := noopCommand()
	cmd.Description = "from zed"
	lateAPI.RegisterCommand("shared", cmd)
	early, earlyAPI := handBuiltFiber("abc")
	cmd = noopCommand()
	cmd.Description = "from abc"
	earlyAPI.RegisterCommand("shared", cmd)
	r.mu.Lock()
	r.fibers["zed"] = late
	r.fibers["abc"] = early
	r.mu.Unlock()

	commands := r.CommandEntries()
	require.Len(t, commands, 1)
	assert.Equal(t, "from abc", commands[0].Description)
}

func TestModelToolsAndActiveFilter(t *testing.T) {
	r := NewRunner("")
	f, api := handBuiltFiber("ext")
	api.RegisterTool(noopTool("ext-tool"))
	r.mu.Lock()
	r.fibers["ext"] = f
	r.mu.Unlock()
	r.SetBaseTools([]tooldef.Tool{{Definition: llm.ToolDefinition{Name: "builtin-tool"}}})

	m := r.snapshot()
	require.Len(t, m.tools, 2)
	assert.Equal(t, "builtin-tool", m.tools[0].Name, "builtin tools lead the list")
	assert.Equal(t, "builtin", m.tools[0].Source)

	r.SetActiveTools([]string{"ext-tool"})
	require.Len(t, r.snapshot().activeTools, 1)
	assert.Equal(t, "ext-tool", r.snapshot().activeTools[0].Name)

	// SetBaseTools copies: the caller's slice is not retained.
	base := []tooldef.Tool{{Definition: llm.ToolDefinition{Name: "mutated"}}}
	r.SetBaseTools(base)
	base[0].Definition.Name = "changed-after-the-fact"
	assert.Equal(t, "mutated", r.snapshot().tools[0].Name)
}

func TestRunnerLifecycleNotifications(t *testing.T) {
	r := NewRunner("")
	var changes int
	r.SetOnChange(func() { changes++ })

	f, _ := handBuiltFiber("alpha")
	r.mu.Lock()
	r.fibers["alpha"] = f
	r.mu.Unlock()
	assert.Equal(t, 0, changes)

	require.NoError(t, r.UnloadExtension("alpha"))
	assert.Equal(t, 1, changes, "unload notifies the host")
	assert.Error(t, r.UnloadExtension("alpha"), "unknown extension")
	assert.Equal(t, 1, changes, "a rejected call must not notify")

	// A failed reload still reports the transition, because the fiber may have
	// flipped to failed and the host has to refresh what it shows.
	bad, _ := handBuiltFiber("beta")
	bad.Discovered = loader.Discovered{
		Manifest: loader.Manifest{Name: "beta", Exec: "/nonexistent/phi-test-ext"},
	}
	r.mu.Lock()
	r.fibers["beta"] = bad
	r.mu.Unlock()
	assert.Error(t, r.ReloadExtension("beta"))
	assert.Equal(t, 2, changes)
	assert.Equal(t, FiberFailed, bad.State())
}

func TestRunnerLoadExtensionRejectsDuplicate(t *testing.T) {
	r := NewRunner(t.TempDir())
	discovered := loader.Discovered{
		Manifest: loader.Manifest{Name: "dup", Exec: "/nonexistent/phi-test-ext"},
	}
	assert.Error(t, r.LoadExtension(t.Context(), discovered))

	f, _ := handBuiltFiber("dup")
	r.mu.Lock()
	r.fibers["dup"] = f
	r.mu.Unlock()

	assert.ErrorContains(t, r.LoadExtension(t.Context(), discovered), "already loaded")
}

func TestRunnerSetMetaReachesContext(t *testing.T) {
	r := NewRunner("")
	f, _ := handBuiltFiber("meta")
	r.mu.Lock()
	r.fibers["meta"] = f
	r.mu.Unlock()

	r.SetMeta("session-1", "/tmp/project")
	ctx := r.context()
	assert.Equal(t, "session-1", ctx.SessionID)
	assert.Equal(t, "/tmp/project", ctx.Cwd)
	assert.Equal(t, "session-1", r.hostOpts().SessionID, "bound host opts carry the session")
}

func TestRunnerCloseUnloadsEverything(t *testing.T) {
	r := NewRunner("")
	f, _ := handBuiltFiber("one")
	r.mu.Lock()
	r.fibers["one"] = f
	r.mu.Unlock()

	r.Close()
	assert.Nil(t, r.fibers)
	assert.Equal(t, FiberDisposed, f.State())
	assert.Empty(t, r.Names())

	r.Close() // idempotent

	// A closed runner must refuse instead of writing into a nil map.
	assert.ErrorContains(t, r.LoadExtension(t.Context(), loader.Discovered{
		Manifest: loader.Manifest{Name: "late", Exec: "/nonexistent/phi-test-ext"},
	}), "closed")
	assert.Empty(t, r.Loaded())
}
