package runtime

import (
	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
)

// PaneStack owns pane real estate: full-screen panes that take the whole
// frame (diff review, code viewer) and bottom panes that replace the composer
// slot (error ask). Registration order is priority within each kind.
type PaneStack struct {
	full   []FullPane
	bottom []BottomPane
}

// FullPane owns the whole frame while active. Handle restores the pane's own
// follow-up (focus return, list refresh) after it closes; the shell does not
// know about pane-specific exits. Close hides the pane — the stack uses it to
// enforce one-frame exclusivity.
type FullPane interface {
	Active() bool
	Close()
	Handle(ctx *components.EventContext, ev xui.Event)
	Draw(ctx components.DrawContext) components.Surface
}

// BottomPane replaces the composer slot while active.
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

// AddFull registers a full-screen pane.
func (ps *PaneStack) AddFull(p FullPane) { ps.full = append(ps.full, p) }

// AddBottom registers a composer-slot pane.
func (ps *PaneStack) AddBottom(p BottomPane) { ps.bottom = append(ps.bottom, p) }

// Active returns the first active full pane in registration order.
func (ps *PaneStack) Active() (FullPane, bool) {
	for _, p := range ps.full {
		if p.Active() {
			return p, true
		}
	}
	return nil, false
}

// CloseOthers hides every full pane except self: two full-screen overlays
// cannot both own the frame, and the one underneath would come back on the
// next Esc.
func (ps *PaneStack) CloseOthers(self FullPane) {
	for _, p := range ps.full {
		if p != self {
			p.Close()
		}
	}
}

// Height returns the first active bottom pane's height request.
func (ps *PaneStack) Height(width int, method xui.WidthMethod) (int, bool) {
	for _, p := range ps.bottom {
		if h, active := p.PreferredHeight(width, method); active {
			return h, true
		}
	}
	return 0, false
}

// Draw renders the first active bottom pane.
func (ps *PaneStack) Draw(ctx components.DrawContext, width, height int) (components.Surface, bool) {
	for _, p := range ps.bottom {
		if surf, ok := p.DrawBottom(ctx, width, height); ok {
			return surf, true
		}
	}
	return components.Surface{}, false
}

// Key routes keys to bottom panes; first handler wins.
func (ps *PaneStack) Key(ctx *components.EventContext, ev xui.KeyEvent) bool {
	for _, p := range ps.bottom {
		if k, ok := p.(KeyPane); ok && k.HandleKey(ctx, ev) {
			return true
		}
	}
	return false
}

// BlocksComposer reports whether any full pane or bottom pane claims input.
func (ps *PaneStack) BlocksComposer() bool {
	for _, p := range ps.full {
		if p.Active() {
			return true
		}
	}
	for _, p := range ps.bottom {
		if p.BlocksComposer() {
			return true
		}
	}
	return false
}
