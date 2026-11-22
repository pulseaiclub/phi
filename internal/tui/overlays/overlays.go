package overlays

import (
	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/ask"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

type overlayComposer interface {
	HideCompleters()
	HidePalette()
	FocusChat()
}

// ShellHooks is the editor surface overlays drive: keyboard focus back to the
// shell, and the error dialog's Retry/Copy side effects. *editor.Editor
// implements it; overlay callbacks stay one aggregate instead of loose funcs.
type ShellHooks interface {
	FocusEditor()
	ResolveErrorReply(reply controller.ErrorReply, text string)
}

// Overlays owns extension-confirm and loop-error UI that replaces the composer slot.
type Overlays struct {
	theme    components.Theme
	confirm  *confirmAskState
	err      *ask.Dialog
	activity *controller.ActivityHandler
	composer overlayComposer
	hooks    ShellHooks
}

// NewOverlays builds overlay state handlers.
func NewOverlays(
	theme components.Theme,
	activity *controller.ActivityHandler,
	composer overlayComposer,
	hooks ShellHooks,
) *Overlays {
	return &Overlays{
		theme:    theme,
		activity: activity,
		composer: composer,
		hooks:    hooks,
	}
}

// SetTheme updates overlay chrome styling.
func (o *Overlays) SetTheme(th components.Theme) {
	o.theme = th
}

// Active reports whether a modal overlay is showing.
func (o *Overlays) Active() bool {
	return o.confirm != nil || o.err != nil
}

// BlocksComposer reports whether composer input should be disabled.
func (o *Overlays) BlocksComposer() bool {
	return o.Active()
}

// BlocksSessionSwitch reports modal blockers independent of activity. Extension
// confirmations use ActivityAwaitingApproval, including its expiry handling.
func (o *Overlays) BlocksSessionSwitch() bool {
	return o.err != nil
}

// Apply routes overlay bus messages by Kind.
func (o *Overlays) Apply(msg controller.OverlayMsg) {
	switch msg.Kind {
	case controller.OverlayExtConfirm:
		o.beginExtConfirm(msg)
	case controller.OverlayExtConfirmDismiss:
		o.dismissExtConfirm()
	case controller.OverlayErrorAsk:
		o.beginErrorAsk(msg)
	case controller.OverlayErrorDismiss:
		o.dismissError()
	}
}

// HandleKey routes keys in the same priority order as DrawBottom.
func (o *Overlays) HandleKey(ctx *components.EventContext, e xui.KeyEvent) bool {
	return o.handleConfirmKey(ctx, e) || o.handleErrorKey(ctx, e)
}

// Cancel resolves pending dialogs with empty replies so waiting callers resume.
func (o *Overlays) Cancel() {
	o.resolveExtConfirm(controller.ExtConfirmReply{})
	o.resolveError(controller.ErrorReply{})
}

// PreferredBottomHeight estimates rows for the bottom overlay or composer slot.
func (o *Overlays) PreferredBottomHeight(width int, method xui.WidthMethod) (height int, overlay bool) {
	if o.confirm != nil {
		return o.confirm.preferredAskHeight(), true
	}
	if o.err != nil {
		return o.err.PreferredHeight(width, method), true
	}
	return 0, false
}

// DrawBottom renders the overlay panel when active.
func (o *Overlays) DrawBottom(ctx components.DrawContext, width, height int) (components.Surface, bool) {
	if o.confirm != nil {
		return o.drawExtConfirm(ctx, width, height), true
	}
	if o.err != nil {
		return o.err.Draw(o.theme, o.theme.Destructive, width, height, ctx.Method), true
	}
	return components.Surface{}, false
}
