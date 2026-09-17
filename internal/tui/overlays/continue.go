package overlays

import (
	"fmt"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

type continueAskState struct {
	maxRounds int
	reply     chan controller.ContinueReply
	selected  int
}

func newContinueAskState(maxRounds int, reply chan controller.ContinueReply) *continueAskState {
	return &continueAskState{
		maxRounds: maxRounds,
		reply:     reply,
		selected:  0,
	}
}

func (st *continueAskState) accent(th components.Theme) (xui.Style, xui.Style) {
	return chrome.DecisionPrimary(th), chrome.ModalBorder(th)
}

func (*continueAskState) preferredHeight(_ int, _ xui.WidthMethod) int {
	h := 2 + 1 + 1 + len(continueOptionLabels) + 1 + 1
	return max(h, 8)
}

func (st *continueAskState) body(
	th components.Theme,
	primary xui.Style,
	innerW int,
	method xui.WidthMethod,
) []components.RichLine {
	body := append(components.WrapSpans([]components.Span{
		{
			Text:  fmt.Sprintf("Reached max tool rounds (%d). Continue for another %d?", st.maxRounds, st.maxRounds),
			Style: th.Foreground,
		},
	}, innerW, method), components.RichLine{})
	for i, label := range continueOptionLabels {
		body = append(body, chrome.OptionLine(th, primary, label, i == st.selected, innerW, method)...)
	}
	body = append(body, components.WrapSpans([]components.Span{
		{Text: chrome.AskHint("↑↓ move", "select", "stop"), Style: th.Muted},
	}, innerW, method)...)
	return body
}

func (o *Overlays) beginContinueAsk(msg controller.OverlayMsg) {
	o.prepareAsk()
	o.cont = newContinueAskState(msg.MaxRounds, msg.ContReply)
}

func (o *Overlays) dismissContinue() {
	if o.cont == nil {
		return
	}
	o.cont = nil
	o.restoreAfterAsk()
}

func (o *Overlays) resolveContinue(r controller.ContinueReply) {
	st := o.cont
	if st == nil {
		return
	}
	o.cont = nil
	o.restoreAfterAsk()
	sendNonBlocking(st.reply, r)
}

func (o *Overlays) drawContinueAsk(ctx components.DrawContext, width, height int) components.Surface {
	if o.cont == nil {
		return components.NewSurface(width, height, nil)
	}
	return o.drawAskPanel(o.cont, ctx, width, height)
}

func (o *Overlays) handleContinueKey(ctx *components.EventContext, e xui.KeyEvent) bool {
	st := o.cont
	if st == nil || !e.Press {
		return false
	}
	return o.handleListAskKey(ctx, e, &st.selected, len(continueOptionLabels),
		func() { o.resolveContinue(controller.ContinueReply{}) },
		func() { o.acceptContinueOption(st.selected) })
}

func (o *Overlays) acceptContinueOption(idx int) {
	switch idx {
	case 0:
		o.resolveContinue(controller.ContinueReply{Continue: true})
	default:
		o.resolveContinue(controller.ContinueReply{})
	}
}
