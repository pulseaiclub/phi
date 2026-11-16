package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/pulseaiclub/phi/internal/debuglog"

	"github.com/pulseaiclub/phi/internal/extension/loader"
)

// Topology mutations reserve or release the map entry, start processes with
// their host capabilities, then notify the host that its derived state is stale.

// LoadExtension loads a single extension and wires it to the host.
func (r *Runner) LoadExtension(ctx context.Context, discovered loader.Discovered) error {
	if r == nil {
		return errors.New("extension: no runner")
	}
	name := discovered.Manifest.Name
	f := NewFiber(name, discovered)

	// Reserve the name before the slow start so two concurrent loads of the
	// same extension cannot both win.
	r.mu.Lock()
	if r.fibers == nil {
		r.mu.Unlock()
		return errors.New("extension: runner is closed")
	}
	if _, exists := r.fibers[name]; exists {
		r.mu.Unlock()
		return fmt.Errorf("extension %q already loaded", name)
	}
	r.fibers[name] = f
	r.invalidateLocked()
	r.mu.Unlock()

	if err := f.Load(ctx, r.startEnv()); err != nil {
		r.mu.Lock()
		delete(r.fibers, name)
		r.invalidateLocked()
		r.mu.Unlock()
		return fmt.Errorf("load %q: %w", name, err)
	}

	r.topologyChanged()
	debuglog.Logf("runner: loaded extension %q", name)
	return nil
}

// UnloadExtension unloads a single extension by name.
func (r *Runner) UnloadExtension(name string) error {
	if r == nil {
		return errors.New("extension: no runner")
	}

	r.mu.Lock()
	f, exists := r.fibers[name]
	if exists {
		delete(r.fibers, name)
		r.invalidateLocked()
	}
	r.mu.Unlock()

	if !exists {
		return fmt.Errorf("extension %q not found", name)
	}

	err := f.Unload()
	r.topologyChanged()
	if err != nil {
		return fmt.Errorf("unload %q: %w", name, err)
	}
	debuglog.Logf("runner: unloaded extension %q", name)
	return nil
}

// ReloadExtension performs a hot swap of a single extension. The replacement is
// started and handshaken before the old process is closed, so a reload that
// fails leaves the running extension serving.
func (r *Runner) ReloadExtension(name string) error {
	if r == nil {
		return errors.New("extension: no runner")
	}
	f := r.Fiber(name)
	if f == nil {
		return fmt.Errorf("extension %q not found", name)
	}

	if err := f.Reload(context.Background(), r.startEnv()); err != nil {
		// The fiber may have flipped to failed (no old process to keep).
		r.topologyChanged()
		return fmt.Errorf("reload %q: %w", name, err)
	}

	r.topologyChanged()
	debuglog.Logf("runner: reloaded extension %q", name)
	return nil
}

// GetExtensionState returns the state of an extension.
func (r *Runner) GetExtensionState(name string) (FiberState, error) {
	if r == nil {
		return FiberDisposed, errors.New("extension: no runner")
	}
	f := r.Fiber(name)
	if f == nil {
		return FiberDisposed, fmt.Errorf("extension %q not found", name)
	}
	return f.State(), nil
}

// ListExtensions returns a snapshot of all extension names and their states.
func (r *Runner) ListExtensions() map[string]FiberState {
	if r == nil {
		return nil
	}
	names := r.Names()
	out := make(map[string]FiberState, len(names))
	for _, name := range names {
		if f := r.Fiber(name); f != nil {
			out[name] = f.State()
		}
	}
	return out
}

// startEnv snapshots what a fiber needs to start a process.
func (r *Runner) startEnv() StartEnv {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return StartEnv{
		LogDir: r.logDir, Cwd: r.cwd, SessionID: r.session,
		Host: r.host,
		Sink: procSink{ui: r.ui, sendUser: r.host.SendUserMessage},
	}
}
