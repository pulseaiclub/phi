package agent

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/llm"
)

// EngineSpec is the complete configuration for one agent runtime: model,
// session, and extension runner. Updates replace the whole value, so a reader
// never observes a partial reconfiguration.
type EngineSpec struct {
	Model      llm.ModelConfig
	Session    *Session
	Extensions *extension.Runner
}

type versionedEngineSpec struct {
	spec    EngineSpec
	version uint64
}

// EngineConfigurator owns engine configuration for exactly one host (the
// controller). It mirrors the OpenTelemetry MeterConfiguratorHandle pattern:
// the host claims the handle once, installs an update callback, and every
// later change publishes a complete EngineSpec through Set.
type EngineConfigurator struct {
	mu         sync.Mutex // serializes Set with the apply callback
	spec       atomic.Pointer[versionedEngineSpec]
	onUpdate   atomic.Pointer[func(EngineSpec)]
	version    uint64 // bumped once per Set; guarded by mu
	registered bool   // claimed by the host; guarded by mu
}

var errConfiguratorAlreadyClaimed = errors.New("agent: EngineConfigurator already claimed")

// NewEngineConfigurator returns a configurator with no spec set.
func NewEngineConfigurator() *EngineConfigurator {
	return &EngineConfigurator{}
}

// Set publishes a complete spec and synchronously applies it through the
// registered callback. Set blocks until the apply finishes, so concurrent
// Sets cannot interleave partial updates.
func (h *EngineConfigurator) Set(spec EngineSpec) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.version++
	h.spec.Store(&versionedEngineSpec{spec: spec, version: h.version})
	if fn := h.onUpdate.Load(); fn != nil {
		(*fn)(spec)
	}
}

// Snapshot returns the latest complete spec and its version, for tests.
func (h *EngineConfigurator) Snapshot() (EngineSpec, uint64) {
	if current := h.spec.Load(); current != nil {
		return current.spec, current.version
	}
	return EngineSpec{}, 0
}

// Register claims the handle for one host and installs its apply callback.
// It returns an error when the handle is already claimed.
func (h *EngineConfigurator) Register(fn func(EngineSpec)) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.registered {
		return errConfiguratorAlreadyClaimed
	}
	h.registered = true
	h.onUpdate.Store(&fn)
	return nil
}
