package lsp

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// State is what a caller can expect for a file without starting anything.
type State string

const (
	// StateOff means the manager is disabled, or no installed server handles
	// this file type.
	StateOff State = "off"
	// StateStarting means a process is spawned and the handshake is in flight.
	StateStarting State = "starting"
	// StateReady means the server for this file type is up.
	StateReady State = "ready"
	// StateFailed means the server for this file type could not be started, or
	// has died for good.
	StateFailed State = "failed"
)

// maxRestarts bounds how often one crashed language server is respawned.
const maxRestarts = 3

// serverError is a best-effort failure. Callers may show it verbatim.
type serverError string

func (e serverError) Error() string { return string(e) }

const (
	errNoServer serverError = "no language server for this file type"
	errClosed   serverError = "language servers are shut down"
)

// Manager owns the language servers of one workspace root.
//
// It is safe for concurrent use. Discovery of installed servers runs in the
// background at New; nothing is spawned until a caller asks a question, and
// State never spawns at all.
type Manager struct {
	root    string
	enabled bool

	mu         sync.Mutex
	byExt      map[string]*serverDef // resolved by discover()
	clients    map[string]*lspClient // server name -> client
	starting   map[string]chan struct{}
	failed     map[string]string
	available  []string
	restarts   map[string]int // crashes recovered from, per server name
	discovered bool           // the first discover() has finished
	closed     bool           // Close ran; no new servers may start
}

// New returns a Manager for the workspace root. When enabled is false the
// manager stays inert: it resolves no servers and spawns nothing.
func New(root string, enabled bool) *Manager {
	m := &Manager{
		root: root, enabled: enabled,
		byExt:    map[string]*serverDef{},
		clients:  map[string]*lspClient{},
		starting: map[string]chan struct{}{},
		failed:   map[string]string{},
	}
	if !enabled {
		return m
	}
	// Discovery runs in the background so starting the harness stays instant.
	go m.discover()
	return m
}

// discover resolves which known servers are installed. The result replaces the
// previous table in one swap, so readers never see a half-built one.
func (m *Manager) discover() {
	dirs := binDirs()
	byExt, available := indexServers(serverDefs(), func(name string) (string, bool) {
		return lookPathIn(name, dirs)
	})
	m.mu.Lock()
	m.byExt, m.available, m.discovered = byExt, available, true
	m.mu.Unlock()
}

func (m *Manager) isDiscovered() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.discovered
}

// Available lists the servers found on this machine, best first.
func (m *Manager) Available() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.available == nil {
		return []string{}
	}
	cp := make([]string, len(m.available))
	copy(cp, m.available)
	return cp
}

// defFor maps a path (relative to the root, or absolute) to the server that
// handles its extension.
func (m *Manager) defFor(path string) *serverDef {
	if !m.enabled {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.byExt[strings.ToLower(filepath.Ext(path))]
}

// State reports what a caller can expect for rel without starting anything, so
// a UI can say "starting" instead of silently showing its own search hits. The
// second result names the server, or explains a failure.
func (m *Manager) State(rel string) (State, string) {
	def := m.defFor(rel)
	if def == nil {
		// Discovery runs in the background at startup. Until it finishes a
		// file type we know about may still get a server, so report starting
		// rather than off.
		if m.enabled && !m.isDiscovered() && len(registryFor(rel)) > 0 {
			return StateStarting, ""
		}
		return StateOff, ""
	}
	m.mu.Lock()
	c, ok := m.clients[def.Name]
	why, bad := m.failed[def.Name]
	_, pending := m.starting[def.Name]
	m.mu.Unlock()

	switch {
	case bad:
		return StateFailed, why
	case pending:
		return StateStarting, def.Name
	case !ok:
		return StateStarting, def.Name // not spawned yet; the next call will
	case c.alive() != nil:
		return StateFailed, def.Name
	}
	return StateReady, def.Name
}

// client returns a started client for rel, spawning one on first use. Callers
// that cannot wait should pass a short context; the spawn continues regardless,
// so a later request finds it ready.
func (m *Manager) client(ctx context.Context, rel string) (*lspClient, error) {
	def := m.defFor(rel)
	if def == nil {
		return nil, errNoServer
	}
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, errClosed
		}
		if why, bad := m.failed[def.Name]; bad {
			m.mu.Unlock()
			return nil, serverError(why)
		}
		c, live := m.clients[def.Name]
		wait, starting := m.starting[def.Name]
		if !live && !starting {
			done := make(chan struct{})
			m.starting[def.Name] = done
			m.mu.Unlock()

			go m.spawn(def, done) //nolint:gosec // G118: the spawn outlives the request that triggered it

			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		m.mu.Unlock()

		if starting {
			select {
			case <-wait:
				continue // loop back and pick up the result
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		// alive() reaches the server's stdin, so it runs outside m.mu: a server
		// that stopped reading must not be able to freeze the whole manager.
		err := c.alive()
		if err == nil {
			return c, nil
		}
		// A crashed server would otherwise take hover, definitions and
		// references down with it for the rest of the session. Start a fresh
		// one, but stop after a few crashes so a server that dies on every
		// request is not respawned forever.
		m.mu.Lock()
		giveUp := false
		if cur, ok := m.clients[def.Name]; ok && cur == c {
			if m.restarts == nil {
				m.restarts = map[string]int{}
			}
			if m.restarts[def.Name] >= maxRestarts {
				giveUp = true
			} else {
				m.restarts[def.Name]++
				delete(m.clients, def.Name)
			}
		}
		m.mu.Unlock()
		if giveUp {
			return nil, err
		}
		go c.shutdown() //nolint:gosec // G118: the crash cleanup must outlive this request
	}
}

func (m *Manager) spawn(def *serverDef, done chan struct{}) {
	// Close may have run while this spawn sat in the queue: starting a process
	// nobody will ever shut down is worse than refusing the question.
	m.mu.Lock()
	if m.closed {
		m.failed[def.Name] = errClosed.Error()
		delete(m.starting, def.Name)
		m.mu.Unlock()
		close(done)
		return
	}
	m.mu.Unlock()

	// The handshake gets a generous budget of its own: some servers do real
	// work before answering initialize.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c := newClient(*def, m.root)
	err := c.start(ctx)

	m.mu.Lock()
	if err == nil && m.closed {
		// Close ran while the handshake was in flight: this process is not in
		// m.clients, so Close could not have shut it down.
		err = errClosed
	}
	if err != nil {
		m.failed[def.Name] = err.Error()
	} else {
		m.clients[def.Name] = c
	}
	delete(m.starting, def.Name)
	m.mu.Unlock()
	if err != nil {
		// A half-started server still owns a process and two goroutines. It
		// never reaches m.clients, so Close would not see it: kill it here.
		c.shutdown()
	}
	close(done)
}

// CloseDoc tells the server a file is no longer needed, so it can free the AST
// and the file text. A file no server has opened is ignored.
func (m *Manager) CloseDoc(abs string) {
	def := m.defFor(abs)
	if def == nil {
		return
	}
	m.mu.Lock()
	c := m.clients[def.Name]
	m.mu.Unlock()
	if c != nil {
		c.closeDoc(abs)
	}
}

// Close shuts every server down and marks the manager closed: a request that
// arrives afterwards fails instead of quietly spawning a process nobody will
// ever shut down.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	clients := make([]*lspClient, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, c)
	}
	m.clients = map[string]*lspClient{}
	m.mu.Unlock()
	for _, c := range clients {
		c.shutdown()
	}
}
