package overlay

import (
	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/layout"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/runtime"
)

type overlayComposer interface {
	HideCompleters()
	HidePalette()
}

// ActivitySink is the activity surface the error dialog drives. Defined here
// so the plugin depends on a consumer-shaped port.
type ActivitySink interface {
	Apply(controller.Activity)
	Current() controller.Activity
}

// Plugin owns the modal error UI that replaces the composer slot.
type Plugin struct {
	theme    components.Theme
	err      *errorAskState
	activity ActivitySink
	composer overlayComposer

	focusEditor    func()
	focusChat      func()
	onErrorResolve func(controller.ErrorReply, string)
}

// New builds overlay state handlers.
func New(
	theme components.Theme,
	activity ActivitySink,
	composer overlayComposer,
	focusEditor, focusChat func(),
	onErrorResolve func(controller.ErrorReply, string),
) *Plugin {
	return &Plugin{
		theme:          theme,
		activity:       activity,
		composer:       composer,
		focusEditor:    focusEditor,
		focusChat:      focusChat,
		onErrorResolve: onErrorResolve,
	}
}

// Register wires the plugin into the runtime: the OverlayMsg subscription,
// theme fan-out, and the composer-slot bottom pane.
func (o *Plugin) Register(rt *runtime.Runtime) {
	runtime.Subscribe(rt, o.Apply)
	rt.AddBottomPane(o)
	rt.OnTheme(o.SetTheme)
}

// SetTheme updates overlay chrome styling.
func (o *Plugin) SetTheme(th components.Theme) {
	o.theme = th
}

// Active reports whether a modal overlay is showing.
func (o *Plugin) Active() bool {
	return o.err != nil
}

// BlocksComposer reports whether composer input should be disabled.
func (o *Plugin) BlocksComposer() bool {
	return o.Active()
}

// BlocksSessionSwitch reports whether an error dialog blocks session changes.
func (o *Plugin) BlocksSessionSwitch() bool {
	return o.err != nil
}

// Apply routes overlay bus messages by Kind.
func (o *Plugin) Apply(msg controller.OverlayMsg) {
	switch msg.Kind {
	case controller.OverlayErrorAsk:
		o.beginErrorAsk(msg)
	case controller.OverlayErrorDismiss:
		o.dismissError()
	}
}

// HandleKey routes keys in the same priority order as DrawBottom.
func (o *Plugin) HandleKey(ctx *components.EventContext, e xui.KeyEvent) bool {
	return o.handleErrorKey(ctx, e)
}

// Cancel dismisses the active error dialog.
func (o *Plugin) Cancel() {
	o.resolveError(controller.ErrorReply{})
}

// PreferredHeight estimates rows for the bottom overlay or composer slot.
func (o *Plugin) PreferredHeight(width int, method xui.WidthMethod) (height int, overlay bool) {
	if o.err != nil {
		return o.err.preferredAskHeight(width, method), true
	}
	return 0, false
}

// DrawBottom renders the overlay panel when active.
func (o *Plugin) DrawBottom(ctx components.DrawContext, width, height int) (components.Surface, bool) {
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
