package core

import (
	"context"
	"errors"
	"fmt"
	"sync"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/internal/debuglog"
	"github.com/pulseaiclub/phi/internal/extension/loader"
)

// FiberState represents the lifecycle state of an extension fiber.
type FiberState uint8

const (
	// FiberPending means the fiber has no process: never started.
	FiberPending FiberState = iota
	// FiberLoading means the fiber is currently starting up.
	FiberLoading
	// FiberActive means the fiber is running and healthy.
	FiberActive
	// FiberFailed means the fiber encountered an error but retains its config.
	FiberFailed
	// FiberDisposed means the fiber has been permanently shut down.
	FiberDisposed
)

func (s FiberState) String() string {
	switch s {
	case FiberPending:
		return "pending"
	case FiberLoading:
		return "loading"
	case FiberActive:
		return "active"
	case FiberFailed:
		return "failed"
	case FiberDisposed:
		return "disposed"
	default:
		return "unknown"
	}
}

// Fiber owns one extension subprocess and its lifecycle.
//
// Two locks, on purpose:
//
//   - opMu serializes lifecycle operations. Starting and stopping block on the
//     child (handshake, shutdown budget), so a reload's check-then-act sequence
//     has to stay atomic across that window. Without it, an unload racing a
//     reload can publish a live process onto a disposed fiber — a process no
//     map entry points at any more, so nothing would ever close it.
//   - mu guards the fields those operations publish (state, proc, api, err) and
//     is the only lock read paths take.
type Fiber struct {
	// Immutable after construction.
	ID         string
	Discovered loader.Discovered

	opMu sync.Mutex

	mu    sync.RWMutex
	proc  *Proc
	api   *ext.API
	state FiberState
	err   error // last start/reload/close failure
}

// StartEnv is everything a fiber needs to spawn its subprocess: where to write
// the child's log, and the session context the child is told about.
type StartEnv struct {
	LogDir    string
	Cwd       string
	SessionID string
	Host      ext.HostOpts
	Sink      HostSink
}

// NewFiber creates a fiber from a discovered extension. No process is started.
func NewFiber(id string, discovered loader.Discovered) *Fiber {
	return &Fiber{ID: id, Discovered: discovered, state: FiberPending}
}

// Attach installs an API and state without a subprocess. Host-side benchmarks
// use it to build hand-made fibers whose read paths match a handshaken one;
// production code must go through Load / Reload instead.
func (f *Fiber) Attach(api *ext.API, state FiberState) {
	f.mu.Lock()
	f.api = api
	f.state = state
	f.mu.Unlock()
}

// State reports the lifecycle state. An Active fiber whose process exited on
// its own reports Failed: the host must not advertise an extension it can no
// longer call.
func (f *Fiber) State() FiberState {
	f.mu.RLock()
	state, proc := f.state, f.proc
	f.mu.RUnlock()
	if state == FiberActive && (proc == nil || proc.Closed()) {
		return FiberFailed
	}
	return state
}

// Error returns the last lifecycle error, or nil.
func (f *Fiber) Error() error {
	f.mu.RLock()
	err, state, proc := f.err, f.state, f.proc
	f.mu.RUnlock()
	if err == nil && state == FiberActive && (proc == nil || proc.Closed()) {
		return errors.New("extension process exited")
	}
	return err
}

// Proc returns the underlying process, or nil when the fiber has none.
func (f *Fiber) Proc() *Proc {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.proc
}

// API returns the extension API, or nil when the fiber has no process.
func (f *Fiber) API() *ext.API {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.api
}

// Load starts the extension process and transitions to Active.
func (f *Fiber) Load(ctx context.Context, env StartEnv) error {
	f.opMu.Lock()
	defer f.opMu.Unlock()

	f.mu.Lock()
	switch f.state {
	case FiberActive:
		f.mu.Unlock()
		return fmt.Errorf("fiber %q already active", f.ID)
	case FiberDisposed:
		f.mu.Unlock()
		return fmt.Errorf("fiber %q is disposed", f.ID)
	}
	f.state = FiberLoading
	f.err = nil
	f.mu.Unlock()

	proc, api, err := f.startProc(ctx, env)
	if err != nil {
		f.mu.Lock()
		f.state = FiberFailed
		f.err = err
		f.mu.Unlock()
		return err
	}

	f.mu.Lock()
	f.proc = proc
	f.api = api
	f.state = FiberActive
	f.mu.Unlock()

	debuglog.Logf("fiber %q: active", f.ID)
	return nil
}

// Reload hot-swaps the process: the replacement is started and handshaken
// before the old process is closed, so a broken build leaves the running
// extension in place. A replacement that fails to start leaves the old process
// Active and records the error — the extension is still serving tools, and
// reporting it as failed would be a lie the UI cannot recover from.
func (f *Fiber) Reload(ctx context.Context, env StartEnv) error {
	f.opMu.Lock()
	defer f.opMu.Unlock()

	f.mu.RLock()
	state, old := f.state, f.proc
	f.mu.RUnlock()

	if state == FiberDisposed {
		return fmt.Errorf("fiber %q is disposed", f.ID)
	}

	proc, api, err := f.startProc(ctx, env)
	if err != nil {
		f.mu.Lock()
		f.err = err
		if old == nil || old.Closed() {
			f.state = FiberFailed
		}
		f.mu.Unlock()
		return err
	}

	f.mu.Lock()
	f.proc = proc
	f.api = api
	f.state = FiberActive
	f.err = nil
	f.mu.Unlock()

	if old != nil {
		// Detached on purpose: the replacement already serves, and closing the
		// old child waits out its shutdown budget. Blocking here would make every
		// reload as slow as the slowest child shutdown.
		go func() {
			if err := old.Close(); err != nil {
				debuglog.Logf("fiber %q: closing replaced process: %v", f.ID, err)
			}
		}()
	}

	debuglog.Logf("fiber %q: reloaded", f.ID)
	return nil
}

// Unload stops the extension process and transitions to Disposed. The process
// handle is detached before the close: once Unload returns from the map no
// reader can reach this fiber through the runner, so the graceful-close window
// is below the state abstraction's resolution.
func (f *Fiber) Unload() error {
	f.opMu.Lock()
	defer f.opMu.Unlock()

	f.mu.Lock()
	if f.state == FiberDisposed {
		f.mu.Unlock()
		return nil
	}
	proc := f.proc
	f.state = FiberDisposed
	f.proc = nil
	f.api = nil
	f.mu.Unlock()

	var err error
	if proc != nil {
		err = proc.Close()
		if err != nil {
			f.mu.Lock()
			f.err = err
			f.mu.Unlock()
		}
	}

	debuglog.Logf("fiber %q: disposed", f.ID)
	return err
}

// startProc spawns the subprocess and builds the API from its handshake.
// Handshake failures surface here, so callers never publish a half-ready process.
func (f *Fiber) startProc(ctx context.Context, env StartEnv) (*Proc, *ext.API, error) {
	proc, err := StartProc(
		ctx,
		f.Discovered.Manifest,
		f.Discovered.Path,
		env.LogDir,
		env.Cwd,
		env.SessionID,
		WithHostSink(env.Sink),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("start: %w", err)
	}
	api := ext.NewAPI()
	api.BindHost(env.Host)
	proc.BuildAPI(api)
	return proc, api, nil
}
