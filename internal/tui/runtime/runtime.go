// Package runtime hosts TUI plugins. The Editor (composition root) constructs
// the Runtime, registers plugins, and keeps only layout composition; plugins
// own their feature state and register capabilities into the Runtime.
//
// Dependency direction is one-way: plugin → runtime. The Runtime never imports
// a plugin package — the compiler enforces this as an import cycle.
//
// The Runtime composes six single-concern components (see the per-file docs):
// Msgs dispatches msgs and drain batches, Submit filters submitted lines,
// Layers stacks overlay surfaces, Panes owns full-frame and composer-slot
// panes, Clock ticks per-frame hooks, Theme fans out theme switches.
package runtime

import (
	"reflect"

	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// Plugin is one UI feature registered into the Runtime. Register is called
// once, during Editor assembly; plugins must be fully constructed before
// registration (no two-phase init).
type Plugin interface {
	Register(rt *Runtime)
}

// Runtime composes the plugin host's single-concern components. Everything
// runs on the UI goroutine; plugin state stays single-goroutine like the rest
// of the shell.
type Runtime struct {
	Msgs   *Dispatcher   // msg subscriptions + drain-batch postludes
	Submit *SubmitChain  // "!cmd"-style submit filters
	Layers *OverlayStack // z-ordered overlay surfaces (toast)
	Panes  *PaneStack    // full-screen + composer-slot panes
	Clock  *FrameClock   // per-frame hooks (spinner tick)
	Theme  *ThemeHub     // theme switch fan-out
}

// New returns a Runtime with every component wired.
func New() *Runtime {
	return &Runtime{
		Msgs:   newDispatcher(),
		Submit: &SubmitChain{},
		Layers: &OverlayStack{},
		Panes:  &PaneStack{},
		Clock:  &FrameClock{},
		Theme:  &ThemeHub{},
	}
}

// Register wires a plugin into the Runtime.
func (rt *Runtime) Register(p Plugin) {
	p.Register(rt)
}

// Subscribe routes msgs of type T to h. Package-level because generic methods
// do not exist in Go; it delegates to the Msgs dispatcher.
func Subscribe[T controller.Msg](rt *Runtime, h func(T)) {
	var zero T
	typ := reflect.TypeOf(zero)
	if typ == nil {
		panic("runtime: Subscribe needs a concrete msg type, not an interface")
	}
	rt.Msgs.add(typ, func(m controller.Msg) {
		h(any(m).(T))
	})
}
