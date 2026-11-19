package runtime

import (
	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
)

// FullPane owns the whole frame while active (diff review, code viewer).
// Handle restores the pane's own follow-up (focus return, list refresh) after
// it closes; the shell does not know about pane-specific exits.
type FullPane interface {
	Active() bool
	Handle(ctx *components.EventContext, ev xui.Event)
	Draw(ctx components.DrawContext) components.Surface
}

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

// AddFullPane registers a full-screen pane; registration order is draw and
// key priority.
func (rt *Runtime) AddFullPane(p FullPane) { rt.fullPanes = append(rt.fullPanes, p) }

// AddBottomPane registers a composer-slot pane; registration order is priority.
func (rt *Runtime) AddBottomPane(p BottomPane) { rt.bottomPanes = append(rt.bottomPanes, p) }

// ActiveFullPane returns the first active full pane in registration order.
func (rt *Runtime) ActiveFullPane() (FullPane, bool) {
	for _, p := range rt.fullPanes {
		if p.Active() {
			return p, true
		}
	}
	return nil, false
}

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
