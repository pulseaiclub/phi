// Package runtime hosts TUI plugins. The Editor (composition root) constructs
// the Runtime, registers plugins, and keeps only layout composition; plugins
// own their feature state and register capabilities into the Runtime.
//
// Dependency direction is one-way: plugin → runtime. The Runtime never imports
// a plugin package — the compiler enforces this as an import cycle.
//
// Contracts:
//   - Msgs: Subscribe routes controller.Msg by concrete type to handlers; they
//     run on the UI goroutine in registration order. DispatchBatch preserves
//     the shell's drain-batch semantics (one Sync per marked batch).
//   - Overlays: AddOverlay layers full-surface draw hooks above the root
//     layout, ordered by z (ascending, stable).
//   - Theme: ApplyTheme fans a theme switch out to subscribers in registration
//     order.
package runtime

import (
	"reflect"
	"slices"

	"github.com/pulseaiclub/xui"

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
	msgs          map[reflect.Type][]func(controller.Msg)
	overlays      []overlayEntry
	theme         []func(components.Theme)
	fullPanes     []FullPane
	bottomPanes   []BottomPane
	submitFilters []SubmitFilter

	batchStart  []BatchStart
	batchEnd    []BatchEnd
	batchMarked bool

	frameHooks []FrameHook
}

// New returns an empty Runtime.
func New() *Runtime {
	return &Runtime{msgs: make(map[reflect.Type][]func(controller.Msg))}
}

// Register wires a plugin into the Runtime.
func (rt *Runtime) Register(p Plugin) {
	p.Register(rt)
}

// --- Msg subscription -------------------------------------------------------

// Subscribe routes msgs of type T to h. Handlers run in registration order;
// one msg activates every handler registered for its type. Registration must
// happen during Editor assembly, before the first Draw drains the bus.
func Subscribe[T controller.Msg](rt *Runtime, h func(T)) {
	var zero T
	typ := reflect.TypeOf(zero)
	if typ == nil {
		panic("runtime: Subscribe needs a concrete msg type, not an interface")
	}
	rt.msgs[typ] = append(rt.msgs[typ], func(m controller.Msg) {
		h(any(m).(T))
	})
}

// Dispatch applies m to the handlers registered for its type and reports
// whether any handler ran. UI goroutine only.
func (rt *Runtime) Dispatch(m controller.Msg) bool {
	hs, ok := rt.msgs[reflect.TypeOf(m)]
	if !ok {
		return false
	}
	for _, h := range hs {
		h(m)
	}
	return true
}

// --- Submit filters ---------------------------------------------------------

// SubmitFilter consumes one submitted text line; true means handled. Filters
// run in registration order before the shell's own submit path — the bash
// plugin claims "!cmd" prefixes here.
type SubmitFilter func(text string) bool

// AddSubmitFilter registers a submit filter.
func (rt *Runtime) AddSubmitFilter(f SubmitFilter) { rt.submitFilters = append(rt.submitFilters, f) }

// FilterSubmit runs the filter chain; true means the text was consumed.
func (rt *Runtime) FilterSubmit(text string) bool {
	for _, f := range rt.submitFilters {
		if f(text) {
			return true
		}
	}
	return false
}

// --- Overlay layers ---------------------------------------------------------

// OverlayDraw contributes one full-surface layer above the root layout;
// ok=false skips the frame (nothing to draw).
type OverlayDraw func(ctx components.DrawContext) (components.Surface, bool)

type overlayEntry struct {
	z    int
	draw OverlayDraw
}

// AddOverlay registers a draw hook layered above the root layout. Higher z
// wins; ties keep registration order.
func (rt *Runtime) AddOverlay(z int, draw OverlayDraw) {
	rt.overlays = append(rt.overlays, overlayEntry{z: z, draw: draw})
}

// Overlays renders every active overlay contribution, ascending by z. The
// caller appends the returned subsurfaces to its root surface.
func (rt *Runtime) Overlays(ctx components.DrawContext) []components.SubSurface {
	if len(rt.overlays) == 0 {
		return nil
	}
	entries := slices.Clone(rt.overlays)
	slices.SortStableFunc(entries, func(a, b overlayEntry) int { return a.z - b.z })
	var out []components.SubSurface
	for _, e := range entries {
		if surf, ok := e.draw(ctx); ok {
			out = append(out, components.SubSurface{
				Origin:  components.Point{X: 0, Y: 0},
				Surface: surf,
				Z:       e.z,
			})
		}
	}
	return out
}

// --- Full-screen panes ------------------------------------------------------

// FullPane owns the whole frame while active (diff review, code viewer).
// Handle restores the pane's own follow-up (focus return, list refresh) after
// it closes; the shell does not know about pane-specific exits. Close hides
// the pane — the runtime uses it to enforce one-frame exclusivity.
type FullPane interface {
	Active() bool
	Close()
	Handle(ctx *components.EventContext, ev xui.Event)
	Draw(ctx components.DrawContext) components.Surface
}

// AddFullPane registers a full-screen pane; registration order is draw and
// key priority.
func (rt *Runtime) AddFullPane(p FullPane) { rt.fullPanes = append(rt.fullPanes, p) }

// ActiveFullPane returns the first active full pane in registration order.
func (rt *Runtime) ActiveFullPane() (FullPane, bool) {
	for _, p := range rt.fullPanes {
		if p.Active() {
			return p, true
		}
	}
	return nil, false
}

// CloseFullPanesExcept hides every full pane except self: two full-screen
// overlays cannot both own the frame, and the one underneath would come back
// on the next Esc.
func (rt *Runtime) CloseFullPanesExcept(self FullPane) {
	for _, p := range rt.fullPanes {
		if p != self {
			p.Close()
		}
	}
}

// --- Bottom panes (composer slot) -------------------------------------------

// BottomPane replaces the composer slot while active (error ask dialog).
type BottomPane interface {
	// PreferredHeight returns the pane's row request and whether it is active.
	PreferredHeight(width int, method xui.WidthMethod) (height int, active bool)
	DrawBottom(ctx components.DrawContext, width, height int) (components.Surface, bool)
	BlocksComposer() bool
}

// KeyPane takes the keyboard while its bottom pane is active. Optional for
// BottomPane implementers that take no input.
type KeyPane interface {
	HandleKey(ctx *components.EventContext, ev xui.KeyEvent) bool
}

// AddBottomPane registers a composer-slot pane; registration order is priority.
func (rt *Runtime) AddBottomPane(p BottomPane) { rt.bottomPanes = append(rt.bottomPanes, p) }

// BottomHeight returns the first active bottom pane's height request.
func (rt *Runtime) BottomHeight(width int, method xui.WidthMethod) (int, bool) {
	for _, p := range rt.bottomPanes {
		if h, active := p.PreferredHeight(width, method); active {
			return h, true
		}
	}
	return 0, false
}

// DrawBottom renders the first active bottom pane.
func (rt *Runtime) DrawBottom(ctx components.DrawContext, width, height int) (components.Surface, bool) {
	for _, p := range rt.bottomPanes {
		if surf, ok := p.DrawBottom(ctx, width, height); ok {
			return surf, true
		}
	}
	return components.Surface{}, false
}

// BottomKey routes keys to bottom panes; first handler wins.
func (rt *Runtime) BottomKey(ctx *components.EventContext, ev xui.KeyEvent) bool {
	for _, p := range rt.bottomPanes {
		if k, ok := p.(KeyPane); ok && k.HandleKey(ctx, ev) {
			return true
		}
	}
	return false
}

// BlocksComposer reports whether any full pane or bottom pane claims input.
func (rt *Runtime) BlocksComposer() bool {
	for _, p := range rt.fullPanes {
		if p.Active() {
			return true
		}
	}
	for _, p := range rt.bottomPanes {
		if p.BlocksComposer() {
			return true
		}
	}
	return false
}

// --- Drain batches ----------------------------------------------------------

// Batch hooks preserve the shell's drain-batch semantics: handlers mark
// session work with MarkBatchEvent, and the postludes run once per batch,
// in the order the composition root registered them. Registration order —
// not plugin registration — decides the Sync → activity → status → stick
// sequence.
type (
	BatchStart func()
	BatchEnd   func()
)

// OnBatchStart registers a hook that runs before every drained batch.
func (rt *Runtime) OnBatchStart(fn BatchStart) { rt.batchStart = append(rt.batchStart, fn) }

// OnBatchEnd registers a postlude that runs after a batch in which some
// handler called MarkBatchEvent.
func (rt *Runtime) OnBatchEnd(fn BatchEnd) { rt.batchEnd = append(rt.batchEnd, fn) }

// MarkBatchEvent flags the current batch as carrying session work. Handlers
// call it on the UI goroutine while their handler runs.
func (rt *Runtime) MarkBatchEvent() { rt.batchMarked = true }

// DispatchBatch applies one drained batch: start hooks, per-msg dispatch,
// then end hooks when the batch was marked.
func (rt *Runtime) DispatchBatch(batch []controller.Msg) {
	if len(batch) == 0 {
		return
	}
	for _, fn := range rt.batchStart {
		fn()
	}
	rt.batchMarked = false
	for _, m := range batch {
		rt.Dispatch(m)
	}
	if rt.batchMarked {
		for _, fn := range rt.batchEnd {
			fn()
		}
	}
}

// --- Frame hooks ------------------------------------------------------------

// FrameHook runs once per UI frame (Editor.Draw), before layout. The activity
// plugin ticks its spinner here; hooks run in registration order.
type FrameHook func()

// OnFrame registers a per-frame hook.
func (rt *Runtime) OnFrame(fn FrameHook) { rt.frameHooks = append(rt.frameHooks, fn) }

// Frame advances one UI frame.
func (rt *Runtime) Frame() {
	for _, fn := range rt.frameHooks {
		fn()
	}
}

// --- Theme fan-out ----------------------------------------------------------

// OnTheme subscribes to theme switches. Callbacks run in registration order.
func (rt *Runtime) OnTheme(fn func(components.Theme)) {
	rt.theme = append(rt.theme, fn)
}

// ApplyTheme fans a theme switch out to every subscriber.
func (rt *Runtime) ApplyTheme(th components.Theme) {
	for _, fn := range rt.theme {
		fn(th)
	}
}
