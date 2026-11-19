// Package runtime hosts TUI plugins. The Editor (composition root) constructs
// the Runtime, registers plugins, and keeps only layout composition; plugins
// own their feature state and register capabilities into the Runtime.
//
// Dependency direction is one-way: plugin → runtime. The Runtime never imports
// a plugin package — the compiler enforces this as an import cycle.
//
// Contracts:
//   - Msgs: Subscribe routes controller.Msg by concrete type to handlers; they
//     run on the UI goroutine in registration order. Editor.drainBus keeps the
//     session-batch semantics (single Sync per batch) until the session plugin
//     migrates with a batch-aware dispatcher.
//   - Overlays: AddOverlay layers full-surface draw hooks above the root
//     layout, ordered by z (ascending, stable).
//   - Theme: ApplyTheme fans a theme switch out to subscribers in registration
//     order.
package runtime

import (
	"reflect"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// Plugin is one UI feature registered into the Runtime. Register is called
// once, during Editor assembly; plugins must be fully constructed before
// registration (no two-phase init).
type Plugin interface {
	Register(rt *Runtime)
}

// Runtime is the plugin host. All methods run on the UI goroutine unless
// noted; plugin state stays single-goroutine like the rest of the shell.
type Runtime struct {
	msgs        map[reflect.Type][]func(controller.Msg)
	overlays    []overlayEntry
	theme       []func(components.Theme)
	fullPanes   []FullPane
	bottomPanes []BottomPane
}

// New returns an empty Runtime.
func New() *Runtime {
	return &Runtime{msgs: make(map[reflect.Type][]func(controller.Msg))}
}

// Register wires a plugin into the Runtime.
func (rt *Runtime) Register(p Plugin) {
	p.Register(rt)
}
