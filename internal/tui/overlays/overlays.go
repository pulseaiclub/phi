// Package overlays owns the permission, continue-ask, and extension-confirm
// modals that replace the composer slot while active. Each ask kind lives in
// its own file (permission.go, continue.go, confirm.go); the shared lifecycle,
// panel framing, and list navigation live in ask.go. This file holds only the
// shell: the Overlays value, bus routing, and the public entry points the
// editor and controller call.
package overlays

import (
	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

type overlayComposer interface {
	HideCompleters()
	HidePalette()
}

// Overlays owns permission, continue-ask, and extension-confirm UI that replaces the composer slot.
type Overlays struct {
	theme    components.Theme
	perm     *permAskState
	cont     *continueAskState
	confirm  *confirmAskState
	activity *controller.ActivityHandler
	composer overlayComposer

	focusEditor func()
	focusChat   func()
}

// NewOverlays builds overlay state handlers.
func NewOverlays(
	theme components.Theme,
	activity *controller.ActivityHandler,
	composer overlayComposer,
	focusEditor, focusChat func(),
) *Overlays {
	return &Overlays{
		theme:       theme,
		activity:    activity,
		composer:    composer,
		focusEditor: focusEditor,
		focusChat:   focusChat,
	}
}

// SetTheme updates overlay chrome styling.
func (o *Overlays) SetTheme(th components.Theme) {
	if o != nil {
		o.theme = th
	}
}

// Active reports whether a modal overlay is showing.
func (o *Overlays) Active() bool {
	return o != nil && (o.perm != nil || o.cont != nil || o.confirm != nil)
}

// BlocksComposer reports whether composer input should be disabled.
func (o *Overlays) BlocksComposer() bool {
	return o.Active()
}

// PermissionActive reports whether the permission overlay is showing.
func (o *Overlays) PermissionActive() bool {
	return o != nil && o.perm != nil
}

// ContinueActive reports whether the continue overlay is showing.
func (o *Overlays) ContinueActive() bool {
	return o != nil && o.cont != nil
}

// ConfirmActive reports whether the extension confirm overlay is showing.
func (o *Overlays) ConfirmActive() bool {
	return o != nil && o.confirm != nil
}

// Apply routes overlay bus messages by Kind.
func (o *Overlays) Apply(msg controller.OverlayMsg) {
	if o == nil {
		return
	}
	switch msg.Kind {
	case controller.OverlayPermissionAsk:
		o.beginPermissionAsk(msg)
	case controller.OverlayPermissionDismiss:
		o.dismissPermission()
	case controller.OverlayContinueAsk:
		o.beginContinueAsk(msg)
	case controller.OverlayContinueDismiss:
		o.dismissContinue()
	case controller.OverlayExtConfirm:
		o.beginExtConfirm(msg)
	case controller.OverlayExtConfirmDismiss:
		o.dismissExtConfirm()
	}
}

// HandlePermissionKey handles keyboard input while permission ask is active.
func (o *Overlays) HandlePermissionKey(ctx *components.EventContext, e xui.KeyEvent) bool {
	return o != nil && o.perm != nil && o.handlePermissionKey(ctx, e)
}

// HandleContinueKey handles keyboard input while continue ask is active.
func (o *Overlays) HandleContinueKey(ctx *components.EventContext, e xui.KeyEvent) bool {
	return o != nil && o.cont != nil && o.handleContinueKey(ctx, e)
}

// HandleConfirmKey handles keyboard input while extension confirm is active.
func (o *Overlays) HandleConfirmKey(ctx *components.EventContext, e xui.KeyEvent) bool {
	return o != nil && o.confirm != nil && o.handleConfirmKey(ctx, e)
}

// ResolvePermission sends a permission reply and clears the overlay.
func (o *Overlays) ResolvePermission(r controller.AskReply) {
	o.resolvePermission(r)
}

// ResolveContinue sends a continue reply and clears the overlay.
func (o *Overlays) ResolveContinue(r controller.ContinueReply) {
	o.resolveContinue(r)
}

// ResolveConfirm sends a confirm reply and clears the overlay.
func (o *Overlays) ResolveConfirm(r controller.ExtConfirmReply) {
	o.resolveExtConfirm(r)
}

// PreferredBottomHeight estimates rows for the bottom overlay or composer slot.
func (o *Overlays) PreferredBottomHeight(width int, method xui.WidthMethod) (height int, overlay bool) {
	if o == nil {
		return 0, false
	}
	switch {
	case o.perm != nil:
		return o.perm.preferredHeight(width, method), true
	case o.cont != nil:
		return o.cont.preferredHeight(width, method), true
	case o.confirm != nil:
		return o.confirm.preferredHeight(width, method), true
	}
	return 0, false
}

// DrawBottom renders the overlay panel when active.
func (o *Overlays) DrawBottom(ctx components.DrawContext, width, height int) (components.Surface, bool) {
	if o == nil {
		return components.Surface{}, false
	}
	switch {
	case o.perm != nil:
		return o.drawPermissionAsk(ctx, width, height), true
	case o.cont != nil:
		return o.drawContinueAsk(ctx, width, height), true
	case o.confirm != nil:
		return o.drawExtConfirm(ctx, width, height), true
	}
	return components.Surface{}, false
}
