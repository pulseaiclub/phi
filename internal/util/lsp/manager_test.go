package lsp

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A disabled manager must never resolve a server, whatever is installed, and
// must fail loudly rather than hang.
func TestDisabledManager(t *testing.T) {
	m := New(t.TempDir(), false)
	ctx := t.Context()

	assert.Equal(t, StateOff, mustState(t, m, "x.go"))
	assert.Empty(t, m.Available())

	_, err := m.Definition(ctx, "/tmp/x.go", "x.go", 1, 0)
	assert.ErrorIs(t, err, errNoServer)

	_, err = m.References(ctx, "/tmp/x.go", "x.go", 1, 0)
	assert.ErrorIs(t, err, errNoServer)

	_, err = m.Hover(ctx, "/tmp/x.go", "x.go", 1, 0)
	assert.ErrorIs(t, err, errNoServer)

	_, err = m.Symbols(ctx, "/tmp/x.go", "x.go")
	assert.ErrorIs(t, err, errNoServer)

	m.CloseDoc("/tmp/x.go") // no server is running: nothing to close
	m.Close()
}

// An enabled manager whose discovery found nothing reports off, and never
// spawns anything from State.
func TestStateOffWithoutInstalledServer(t *testing.T) {
	m := New(t.TempDir(), false)
	m.mu.Lock()
	m.enabled, m.discovered = true, true
	m.mu.Unlock()

	state, why := m.State("x.go")
	assert.Equal(t, StateOff, state)
	assert.Empty(t, why)
	assert.Empty(t, m.Available())
}

// Before discovery finishes a known file type may still get a server, so the
// state is starting rather than off.
func TestStateStartingWhileDiscovering(t *testing.T) {
	m := New(t.TempDir(), false)
	m.mu.Lock()
	m.enabled = true
	m.mu.Unlock()

	state, why := m.State("x.go")
	assert.Equal(t, StateStarting, state)
	assert.Empty(t, why)

	// A file type no registry entry claims is off even mid-discovery.
	assert.Equal(t, StateOff, mustState(t, m, "notes.unknownext"))
}

func TestIndexServersPrecedence(t *testing.T) {
	installed := func(names ...string) func(string) (string, bool) {
		lookup := map[string]string{}
		for _, n := range names {
			lookup[n] = "/usr/bin/" + n
		}
		return func(name string) (string, bool) {
			bin, ok := lookup[name]
			return bin, ok
		}
	}

	// pyright is listed before pylsp and ruff, so it wins both Python
	// extensions and the others claim nothing.
	byExt, available := indexServers(serverDefs(), installed("pyright-langserver", "pylsp", "ruff"))
	require.Contains(t, byExt, ".py")
	assert.Equal(t, "pyright", byExt[".py"].Name)
	assert.Equal(t, "pyright", byExt[".pyi"].Name)
	assert.Equal(t, []string{"/usr/bin/pyright-langserver", "--stdio"}, byExt[".py"].Cmd)
	assert.Equal(t, []string{"pyright"}, available)

	// Without pyright the next entry takes over both extensions, and the ones
	// behind it claim nothing at all.
	byExt, available = indexServers(serverDefs(), installed("pylsp", "ruff"))
	require.Contains(t, byExt, ".py")
	assert.Equal(t, "pylsp", byExt[".py"].Name)
	assert.Equal(t, "pylsp", byExt[".pyi"].Name)
	assert.Equal(t, []string{"pylsp"}, available)

	// Extensions nobody installed resolve to nothing.
	byExt, available = indexServers(serverDefs(), installed("gopls"))
	assert.Equal(t, "gopls", byExt[".go"].Name)
	assert.NotContains(t, byExt, ".rs")
	assert.Equal(t, []string{"gopls"}, available)
}

func TestRegistryForOrder(t *testing.T) {
	var names []string
	for _, def := range registryFor("a.py") {
		names = append(names, def.Name)
	}
	assert.Equal(t, []string{"pyright", "pylsp", "ruff"}, names)

	require.NotEmpty(t, registryFor("a.go"))
	assert.Equal(t, "gopls", registryFor("a.go")[0].Name)
	assert.Empty(t, registryFor("a.unknownext"))
}

func TestServerDefsReturnsAFreshTable(t *testing.T) {
	first := serverDefs()
	require.NotEmpty(t, first)
	first[0].Name = "mutated"
	first[0].Cmd[0] = "mutated"

	assert.Equal(t, "gopls", serverDefs()[0].Name)
	assert.Equal(t, "gopls", serverDefs()[0].Cmd[0])
}

// A crashed server is replaced by a fresh spawn, a bounded number of times.
// The stand-in binary does not exist, so starting it fails at once and no real
// language server is ever launched.
func TestCrashedServerIsRespawned(t *testing.T) {
	def := serverDef{Name: "gone", Cmd: []string{"phi-test-no-such-language-server"}, Exts: []string{".go"}}
	dead := func() *lspClient {
		c := newClient(def, t.TempDir())
		c.fail(errors.New("gone exited"))
		return c
	}
	m := &Manager{
		root: t.TempDir(), enabled: true,
		byExt:    map[string]*serverDef{".go": &def},
		clients:  map[string]*lspClient{"gone": dead()},
		starting: map[string]chan struct{}{},
		failed:   map[string]string{},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	// The respawn is attempted; the binary does not exist, so it fails to start.
	_, err := m.client(ctx, "x.go")
	require.Error(t, err)
	assert.NotErrorIs(t, err, serverError("gone exited"), "the stale crash must not be reported")
	assert.Equal(t, 1, m.restarts["gone"])
	assert.Equal(t, StateFailed, mustState(t, m, "x.go"))

	// Out of budget: the crash is reported and nothing is spawned.
	m.mu.Lock()
	delete(m.failed, "gone")
	m.clients["gone"] = dead()
	m.restarts["gone"] = maxRestarts
	m.mu.Unlock()

	_, err = m.client(ctx, "x.go")
	require.Error(t, err)
	assert.EqualError(t, err, "gone exited")
}

func mustState(t *testing.T, m *Manager, rel string) State {
	t.Helper()
	state, _ := m.State(rel)
	return state
}

// After Close the manager refuses to spawn: a request that arrives while the
// app is shutting down must not leave a server process behind.
func TestClosedManagerRefusesToSpawn(t *testing.T) {
	root := t.TempDir()
	m := New(root, false)
	m.mu.Lock()
	m.enabled, m.discovered = true, true
	m.byExt[".go"] = &serverDef{Name: "fake", Cmd: []string{"definitely-not-installed"}, Exts: []string{".go"}}
	m.mu.Unlock()

	m.Close()

	_, err := m.Definition(t.Context(), filepath.Join(root, "a.go"), "a.go", 1, 0)
	assert.ErrorIs(t, err, errClosed)
	assert.Empty(t, m.Available())
}

// A spawn that finishes after Close must be shut down, not registered: Close
// cannot see a client that was not in m.clients when it ran.
func TestSpawnAfterCloseIsShutDown(t *testing.T) {
	m := New(t.TempDir(), false)
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()

	def := &serverDef{Name: "fake", Cmd: []string{"definitely-not-installed"}}
	done := make(chan struct{})
	m.spawn(def, done)
	<-done

	m.mu.Lock()
	_, registered := m.clients[def.Name]
	why := m.failed[def.Name]
	m.mu.Unlock()
	assert.False(t, registered)
	assert.ErrorIs(t, serverError(why), errClosed)
}
