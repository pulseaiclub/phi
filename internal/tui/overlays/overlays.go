package overlays

import (
	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/layout"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

type overlayComposer interface {
	HideCompleters()
	HidePalette()
}

// Overlays owns modal error UI that replaces the composer slot.
type Overlays struct {
	theme    components.Theme
	err      *errorAskState
	activity *controller.ActivityHandler
	composer overlayComposer

	focusEditor    func()
	focusChat      func()
	onErrorResolve func(controller.ErrorReply, string)
}

// NewOverlays builds overlay state handlers.
func NewOverlays(
	theme components.Theme,
	activity *controller.ActivityHandler,
	composer overlayComposer,
	focusEditor, focusChat func(),
	onErrorResolve func(controller.ErrorReply, string),
) *Overlays {
	return &Overlays{
		theme:          theme,
		activity:       activity,
		composer:       composer,
		focusEditor:    focusEditor,
		focusChat:      focusChat,
		onErrorResolve: onErrorResolve,
	}
}

// SetTheme updates overlay chrome styling.
func (o *Overlays) SetTheme(th components.Theme) {
	o.theme = th
}

// Active reports whether a modal overlay is showing.
func (o *Overlays) Active() bool {
	return o.err != nil
}

// BlocksComposer reports whether composer input should be disabled.
func (o *Overlays) BlocksComposer() bool {
	return o.Active()
}

// BlocksSessionSwitch reports whether an error dialog blocks session changes.
func (o *Overlays) BlocksSessionSwitch() bool {
	return o.err != nil
}

// Apply routes overlay bus messages by Kind.
func (o *Overlays) Apply(msg controller.OverlayMsg) {
	switch msg.Kind {
	case controller.OverlayErrorAsk:
		o.beginErrorAsk(msg)
	case controller.OverlayErrorDismiss:
		o.dismissError()
	}
}

// HandleKey routes keys in the same priority order as DrawBottom.
func (o *Overlays) HandleKey(ctx *components.EventContext, e xui.KeyEvent) bool {
	return o.handleErrorKey(ctx, e)
}

// Cancel dismisses the active error dialog.
func (o *Overlays) Cancel() {
	o.resolveError(controller.ErrorReply{})
}

// PreferredBottomHeight estimates rows for the bottom overlay or composer slot.
func (o *Overlays) PreferredBottomHeight(width int, method xui.WidthMethod) (height int, overlay bool) {
	if o.err != nil {
		return o.err.preferredAskHeight(width, method), true
	}
	return 0, false
}

// DrawBottom renders the overlay panel when active.
func (o *Overlays) DrawBottom(ctx components.DrawContext, width, height int) (components.Surface, bool) {
	if o.err != nil {
		return o.drawError(ctx, width, height), true
	}
	return components.Surface{}, false
}

func paintAskPanel(
	body []components.RichLine,
	width, height int,
	border xui.Style,
	method xui.WidthMethod,
) components.Surface {
	panel := components.NewSurface(width, height, nil)
	layout.DrawRoundedBorder(&panel, layout.BorderRounded, border, nil, nil, nil, nil, method)
	y := 1
	for _, line := range body {
		if y >= height-1 {
			break
		}
		components.PaintSpans(&panel, 2, y, line, method)
		y++
	}
	return panel
}
