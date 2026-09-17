package overlays

import (
	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/layout"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// askPanel is the shared contract for the permission, continue, and
// extension-confirm overlays. Each kind supplies its own body and accent
// colors; the lifecycle, panel framing, and list navigation are shared.
type askPanel interface {
	preferredHeight(width int, method xui.WidthMethod) int
	accent(th components.Theme) (primary, border xui.Style)
	body(th components.Theme, primary xui.Style, innerW int, method xui.WidthMethod) []components.RichLine
}

// prepareAsk is the shared begin preamble: abort any currently active ask
// (sending an empty reply on each), hide composer completers, raise the
// activity to "awaiting approval", and focus the editor for key input.
func (o *Overlays) prepareAsk() {
	if o.perm != nil {
		o.resolvePermission(controller.AskReply{})
	}
	if o.cont != nil {
		o.resolveContinue(controller.ContinueReply{})
	}
	if o.confirm != nil {
		o.resolveExtConfirm(controller.ExtConfirmReply{})
	}
	if o.composer != nil {
		o.composer.HideCompleters()
		o.composer.HidePalette()
	}
	o.activity.Apply(controller.ActivityAwaitingApproval)
	if o.focusEditor != nil {
		o.focusEditor()
	}
}

// restoreAfterAsk is the shared dismiss/resolve tail: drop activity back to
// tools (if it was raised) and return keyboard focus to the composer.
func (o *Overlays) restoreAfterAsk() {
	if o.activity != nil && o.activity.Current == controller.ActivityAwaitingApproval {
		o.activity.Apply(controller.ActivityTools)
	}
	if o.focusChat != nil {
		o.focusChat()
	}
}

// sendNonBlocking delivers a reply without blocking; the ask is already
// dismissed, so a full channel is treated as a dropped stale reply.
func sendNonBlocking[T any](ch chan T, v T) {
	if ch == nil {
		return
	}
	select {
	case ch <- v:
	default:
	}
}

// drawAskPanel renders the shared modal frame around a kind-specific body.
func (o *Overlays) drawAskPanel(p askPanel, ctx components.DrawContext, width, height int) components.Surface {
	th := o.theme
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = p.preferredHeight(width, ctx.Method)
	}
	innerW := width - 4
	if innerW < 10 {
		innerW = width
	}
	primary, border := p.accent(th)
	return paintAskPanel(p.body(th, primary, innerW, ctx.Method), width, height, border, ctx.Method)
}

// handleListAskKey drives the shared list navigation shared by the permission
// and continue asks: escape resolves empty, arrows/j/k/h/l move the selection
// (wrapping), enter accepts. Kind-specific accept and resolve come in as
// closures so the typed replies stay local to each ask.
func (o *Overlays) handleListAskKey(
	ctx *components.EventContext, e xui.KeyEvent, sel *int, n int,
	resolveEmpty, accept func(),
) bool {
	if !e.Press {
		return false
	}
	switch e.Code {
	case xui.KeyEscape:
		resolveEmpty()
		ctx.ConsumeAndRedraw()
		return true
	case xui.KeyUp, xui.KeyLeft:
		*sel = (*sel - 1 + n) % n
		ctx.ConsumeAndRedraw()
		return true
	case xui.KeyDown, xui.KeyRight, xui.KeyTab:
		*sel = (*sel + 1) % n
		ctx.ConsumeAndRedraw()
		return true
	case xui.KeyEnter:
		accept()
		ctx.ConsumeAndRedraw()
		return true
	case xui.KeyRune:
		if e.Mods.Has(xui.ModCtrl) || e.Mods.Has(xui.ModAlt) {
			ctx.ConsumeAndRedraw()
			return true
		}
		switch e.Rune {
		case 'k', 'K', 'h', 'H':
			*sel = (*sel - 1 + n) % n
			ctx.ConsumeAndRedraw()
			return true
		case 'j', 'J', 'l', 'L':
			*sel = (*sel + 1) % n
			ctx.ConsumeAndRedraw()
			return true
		}
	}
	ctx.ConsumeAndRedraw()
	return true
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

// askOption labels for the permission ask.
var askOptionLabels = []string{
	"Approve",
	"Allow All for This Session",
	"Allow All for Every Session",
	"Deny with feedback",
}

// continueOptionLabels for the continue ask.
var continueOptionLabels = []string{
	"Continue",
	"Stop",
}
