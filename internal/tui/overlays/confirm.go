package overlays

import (
	"strings"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

type confirmAskState struct {
	title    string
	message  string
	yes      string
	no       string
	danger   bool
	selected int // 0 = yes, 1 = no
	reply    chan controller.ExtConfirmReply
}

func newConfirmAskState(msg controller.OverlayMsg) *confirmAskState {
	yes := strings.TrimSpace(msg.Yes)
	if yes == "" {
		yes = "Yes"
	}
	no := strings.TrimSpace(msg.No)
	if no == "" {
		no = "No"
	}
	return &confirmAskState{
		title:    strings.TrimSpace(msg.Title),
		message:  strings.TrimSpace(msg.Message),
		yes:      yes,
		no:       no,
		danger:   msg.Danger,
		selected: 0,
		reply:    msg.ConfirmReply,
	}
}

func (s *confirmAskState) accent(th components.Theme) (xui.Style, xui.Style) {
	if s.danger {
		return th.Destructive, th.Destructive
	}
	return chrome.DecisionPrimary(th), chrome.ModalBorder(th)
}

func (s *confirmAskState) preferredHeight(_ int, _ xui.WidthMethod) int {
	h := 2 + 1 + 1 + 2 + 1 + 1
	if s.message != "" {
		h += 1 + strings.Count(s.message, "\n")
	}
	if h < 8 {
		return 8
	}
	if h > 16 {
		return 16
	}
	return h
}

func (s *confirmAskState) body(
	th components.Theme,
	primary xui.Style,
	innerW int,
	method xui.WidthMethod,
) []components.RichLine {
	var body []components.RichLine
	add := func(spans ...components.Span) {
		body = append(body, components.WrapSpans(spans, innerW, method)...)
	}
	title := s.title
	if title == "" {
		title = "Confirm"
	}
	add(components.Span{Text: title, Style: th.Foreground})
	if s.message != "" {
		for line := range strings.SplitSeq(s.message, "\n") {
			add(components.Span{Text: line, Style: th.Muted})
		}
	}
	body = append(body, components.RichLine{})
	for i, label := range []string{s.yes, s.no} {
		body = append(body, chrome.OptionLine(th, primary, label, i == s.selected, innerW, method)...)
	}
	body = append(body, components.WrapSpans([]components.Span{
		{Text: chrome.ConfirmHint(), Style: th.Muted},
	}, innerW, method)...)
	return body
}

func (o *Overlays) beginExtConfirm(msg controller.OverlayMsg) {
	o.prepareAsk()
	o.confirm = newConfirmAskState(msg)
}

func (o *Overlays) dismissExtConfirm() {
	if o.confirm == nil {
		return
	}
	o.confirm = nil
	o.restoreAfterAsk()
}

func (o *Overlays) resolveExtConfirm(r controller.ExtConfirmReply) {
	st := o.confirm
	if st == nil {
		return
	}
	o.confirm = nil
	o.restoreAfterAsk()
	sendNonBlocking(st.reply, r)
}

func (o *Overlays) drawExtConfirm(ctx components.DrawContext, width, height int) components.Surface {
	if o.confirm == nil {
		return components.NewSurface(width, height, nil)
	}
	return o.drawAskPanel(o.confirm, ctx, width, height)
}

func (o *Overlays) handleConfirmKey(ctx *components.EventContext, e xui.KeyEvent) bool {
	st := o.confirm
	if st == nil || !e.Press {
		return false
	}
	switch e.Code {
	case xui.KeyEscape:
		o.resolveExtConfirm(controller.ExtConfirmReply{OK: false})
	case xui.KeyLeft, xui.KeyUp:
		st.selected = 0
	case xui.KeyRight, xui.KeyDown, xui.KeyTab:
		st.selected = 1
	case xui.KeyEnter:
		o.resolveExtConfirm(controller.ExtConfirmReply{OK: st.selected == 0})
	case xui.KeyRune:
		if e.Mods.Has(xui.ModCtrl) || e.Mods.Has(xui.ModAlt) {
			break
		}
		switch e.Rune {
		case 'y', 'Y':
			o.resolveExtConfirm(controller.ExtConfirmReply{OK: true})
		case 'n', 'N':
			o.resolveExtConfirm(controller.ExtConfirmReply{OK: false})
		case 'h', 'H', 'k', 'K':
			st.selected = 0
		case 'l', 'L', 'j', 'J':
			st.selected = 1
		}
	}
	ctx.ConsumeAndRedraw()
	return true
}
