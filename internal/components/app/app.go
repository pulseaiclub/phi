package app

import (
	"time"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chat"
	"github.com/pulseaiclub/phi/internal/components/palette"
)

// App is the vxfw-style application runtime.
type App struct {
	vx       *xui.XUI
	loop     *xui.Loop
	root     components.Widget
	focused  components.Widget
	lastSurf components.Surface
	redraw   bool
	// Anim requests a redraw on every frame tick (spinners, etc).
	Anim bool
	// pending is a single push-back slot used when coalesceWheel peeks past a
	// non-wheel event (must not Post to the end of the queue — that reorders).
	pending xui.Event
}

// NewApp creates an App around an existing Vaxis.
func NewApp(vx *xui.XUI) *App {
	return &App{vx: vx, redraw: true}
}

// RequestRedraw schedules a frame from any goroutine (stream updates, etc).
func (a *App) RequestRedraw() {
	if a.loop != nil {
		a.loop.Post(xui.TickEvent{})
		return
	}
	a.redraw = true
}

// Run starts the event loop and drives root until quit.
func (a *App) Run(root components.Widget) error {
	a.root = root
	a.loop = xui.NewLoop(a.vx)
	a.loop.Start()
	defer a.loop.Stop()

	if err := a.vx.EnterAltScreen(); err != nil {
		return err
	}
	a.vx.NotifyWinsize(a.loop)
	a.vx.QueryTerminal(500 * time.Millisecond)
	_ = a.vx.EnableMouse()

	// Init event
	ctx := &components.EventContext{Redraw: true}
	a.dispatch(ctx, xui.FocusEvent{Focused: true})
	if ctx.Focus != nil {
		a.focused = ctx.Focus
	}
	a.redraw = true
	if err := a.draw(); err != nil {
		return err
	}
	a.redraw = false

	ticker := time.NewTicker(time.Second / 60)
	defer ticker.Stop()

	for {
		var ev xui.Event
		if a.pending != nil {
			ev = a.pending
			a.pending = nil
		} else {
			select {
			case ev = <-a.loop.Events():
			case <-ticker.C:
				if a.Anim {
					a.redraw = true
				}
				if a.redraw {
					if err := a.draw(); err != nil {
						return err
					}
					a.redraw = false
				}
				continue
			}
		}
		ev = a.coalesceWheel(ev)
		if a.handleEvent(ev) {
			return nil
		}
		if a.redraw {
			if err := a.draw(); err != nil {
				return err
			}
			a.redraw = false
		}
	}
}

// coalesceWheel merges back-to-back wheel events into one with a summed Wheel
// count so a fast trackpad flick triggers a single redraw instead of dozens of
// partial paints (which leave CJK/ASCII ghost columns on the TTY).
func (a *App) coalesceWheel(ev xui.Event) xui.Event {
	m, ok := ev.(xui.MouseEvent)
	if !ok || (m.Button != xui.MouseWheelUp && m.Button != xui.MouseWheelDown) {
		return ev
	}
	if m.Wheel <= 0 {
		m.Wheel = 1
	}
	for {
		next, ok := a.loop.TryEvent()
		if !ok {
			break
		}
		n, ok := next.(xui.MouseEvent)
		if !ok || (n.Button != xui.MouseWheelUp && n.Button != xui.MouseWheelDown) {
			a.pending = next
			break
		}
		step := n.Wheel
		if step <= 0 {
			step = 1
		}
		if n.Button == m.Button {
			m.Wheel += step
			continue
		}
		// Opposite direction: net the deltas onto the surviving button.
		if m.Wheel > step {
			m.Wheel -= step
			continue
		}
		if m.Wheel < step {
			m.Button = n.Button
			m.Wheel = step - m.Wheel
			continue
		}
		m.Wheel = 0
	}
	if m.Wheel == 0 {
		m.Button = xui.MouseNone
		m.Action = xui.MouseMotion
	}
	// Full refresh heals any prior TTY desync before the scrolled frame paints.
	a.vx.QueueRefresh()
	return m
}

func (a *App) handleEvent(ev xui.Event) (quit bool) {
	ctx := &components.EventContext{}
	switch e := ev.(type) {
	case xui.ResizeEvent:
		a.vx.Resize(e.Cols, e.Rows)
		ctx.Redraw = true
	case xui.KeyEvent:
		if e.CtrlC() {
			return true
		}
		a.dispatch(ctx, e)
	case xui.TickEvent:
		ctx.Redraw = true
	case xui.MouseEvent:
		hit, lx, ly := a.lastSurf.HitTestAt(e.X, e.Y)
		if hit != nil {
			// Only text-entry widgets take keyboard focus. Transcript rows
			// handle clicks themselves (the pane expands or selects), and used
			// to steal focus — leaving the composer cursor visible but dead.
			if e.Action == xui.MousePress {
				if acceptsKeyboardFocus(hit) {
					a.focused = hit
				} else if a.focused != nil && !acceptsKeyboardFocus(a.focused) {
					// Drop stale focus on list rows so keys bubble to the composer.
					a.focused = a.root
				}
			}
			local := e
			local.X, local.Y = lx, ly
			hit.Handle(ctx, local)
			if ctx.Consume {
				break
			}
		}
		// Bubble unconsumed mouse (absolute coords) so root can run selection / overlays.
		a.dispatch(ctx, e)
	default:
		a.dispatch(ctx, ev)
	}
	if ctx.Focus != nil {
		a.focused = ctx.Focus
		ctx.Redraw = true
	}
	if ctx.Quit {
		return true
	}
	if ctx.Redraw {
		a.redraw = true
	}
	return false
}

func (a *App) dispatch(ctx *components.EventContext, ev xui.Event) {
	// Capture → target → bubble (simplified: focused then root).
	// If the focused widget is no longer in the painted tree (full-screen
	// overlay), drop it so keys don't keep going to a hidden ChatInput.
	if a.focused != nil && a.focused != a.root {
		if !surfaceHasWidget(a.lastSurf, a.focused) {
			a.focused = a.root
		} else {
			a.focused.Handle(ctx, ev)
			if ctx.Consume {
				return
			}
		}
	}
	if a.root != nil {
		a.root.Handle(ctx, ev)
	}
}

func surfaceHasWidget(s components.Surface, w components.Widget) bool {
	if w == nil {
		return false
	}
	if s.Widget == w {
		return true
	}
	for _, ch := range s.Children {
		if surfaceHasWidget(ch.Surface, w) {
			return true
		}
	}
	return false
}

// RequestFocus moves keyboard focus to w (nil = root). Safe from the UI goroutine.
func (a *App) RequestFocus(w components.Widget) {
	if w == nil {
		w = a.root
	}
	a.focused = w
	a.redraw = true
}

// acceptsKeyboardFocus reports whether a mouse-press target should become the
// keyboard focus. Message-list rows handle clicks (expand/select) but typing
// must stay on the composer / palette / text fields.
func acceptsKeyboardFocus(w components.Widget) bool {
	switch w.(type) {
	case *chat.ChatInput, *palette.CommandPalette:
		return true
	default:
		return false
	}
}

func (a *App) draw() error {
	cols, rows := a.vx.Screen().Size()
	ctx := components.DrawContext{
		Min:    components.Size{},
		Max:    components.Size{Width: cols, Height: rows},
		Method: xui.WidthUnicode,
	}
	surf := a.root.Draw(ctx)
	a.lastSurf = surf
	win := a.vx.Window()
	win.Clear()
	if cur := surf.Render(win); cur != nil {
		a.vx.Screen().SetCursor(cur.X, cur.Y)
	} else {
		a.vx.Screen().ClearCursor()
	}
	return a.vx.Render()
}
