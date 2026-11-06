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

// bodyLines is the one description of the panel: drawing and height estimation
// both read it, so a wrapped prompt is never measured as a single row.
func (s *confirmAskState) bodyLines(th components.Theme, innerW int, method xui.WidthMethod) []components.RichLine {
	var body []components.RichLine
	add := func(spans ...components.Span) {
		body = append(body, components.WrapSpans(spans, innerW, method)...)
	}

	title := s.title
	if title == "" {
		title = "Confirm"
	}
	add(components.Span{Text: title, Style: chrome.PanelTitle(th)})
	if s.message != "" {
		for line := range strings.SplitSeq(s.message, "\n") {
			add(components.Span{Text: line, Style: th.Muted})
		}
	}
	body = append(body, components.RichLine{})

	primary := chrome.DecisionPrimary(th)
	if s.danger {
		// A destructive confirm reads destructive top to bottom: border and
		// decision rows share the alarm color.
		primary = th.Destructive
	}
	for i, label := range []string{s.yes, s.no} {
		body = append(body, chrome.OptionLine(th, primary, label, i == s.selected, innerW, method)...)
	}
	body = append(body, components.WrapSpans([]components.Span{
		{Text: chrome.ConfirmHint(), Style: th.Muted},
	}, innerW, method)...)
	return body
}

func (s *confirmAskState) preferredAskHeight(width int, method xui.WidthMethod) int {
	if s == nil {
		return 8
	}
	innerW := max(width-4, 20)
	h := len(s.bodyLines(components.DefaultTheme(), innerW, method)) + 2
	if h > 16 {
		return 16
	}
	return max(h, 8)
}

func (o *Overlays) beginExtConfirm(msg controller.OverlayMsg) {
	if o.confirm != nil {
		o.resolveExtConfirm(controller.ExtConfirmReply{})
	}
	if o.perm != nil {
		o.resolvePermission(controller.AskReply{})
	}
	if o.cont != nil {
		o.resolveContinue(controller.ContinueReply{})
	}
	if o.err != nil {
		o.resolveError(controller.ErrorReply{})
	}
	if o.composer != nil {
		o.composer.HideCompleters()
		o.composer.HidePalette()
	}
	o.confirm = newConfirmAskState(msg)
	o.activity.Apply(controller.ActivityAwaitingApproval)
	if o.focusEditor != nil {
		o.focusEditor()
	}
}

func (o *Overlays) dismissExtConfirm() {
	was := o.confirm != nil
	o.confirm = nil
	if !was {
		return
	}
	if o.activity != nil && o.activity.Current == controller.ActivityAwaitingApproval {
		o.activity.Apply(controller.ActivityTools)
	}
	if o.focusChat != nil {
		o.focusChat()
	}
}

func (o *Overlays) resolveExtConfirm(r controller.ExtConfirmReply) {
	st := o.confirm
	if st == nil {
		return
	}
	o.confirm = nil
	if o.activity != nil && o.activity.Current == controller.ActivityAwaitingApproval {
		o.activity.Apply(controller.ActivityTools)
	}
	if o.focusChat != nil {
		o.focusChat()
	}
	if st.reply != nil {
		select {
		case st.reply <- r:
		default:
		}
	}
}

// HandleConfirmKey handles keyboard input while extension confirm is active.
func (o *Overlays) HandleConfirmKey(ctx *components.EventContext, e xui.KeyEvent) bool {
	return o != nil && o.confirm != nil && o.handleConfirmKey(ctx, e)
}

// ConfirmActive reports whether the extension confirm overlay is showing.
func (o *Overlays) ConfirmActive() bool {
	return o != nil && o.confirm != nil
}

// ResolveConfirm sends a confirm reply and clears the overlay.
func (o *Overlays) ResolveConfirm(r controller.ExtConfirmReply) {
	o.resolveExtConfirm(r)
}

func (o *Overlays) handleConfirmKey(ctx *components.EventContext, e xui.KeyEvent) bool {
	st := o.confirm
	if st == nil || !e.Press {
		return false
	}
	switch e.Code {
	case xui.KeyEscape:
		o.resolveExtConfirm(controller.ExtConfirmReply{OK: false})
		ctx.ConsumeAndRedraw()
		return true
	case xui.KeyLeft, xui.KeyUp:
		st.selected = 0
		ctx.ConsumeAndRedraw()
		return true
	case xui.KeyRight, xui.KeyDown, xui.KeyTab:
		st.selected = 1
		ctx.ConsumeAndRedraw()
		return true
	case xui.KeyEnter:
		o.resolveExtConfirm(controller.ExtConfirmReply{OK: st.selected == 0})
		ctx.ConsumeAndRedraw()
		return true
	case xui.KeyRune:
		if e.Mods.Has(xui.ModCtrl) || e.Mods.Has(xui.ModAlt) {
			ctx.ConsumeAndRedraw()
			return true
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
		ctx.ConsumeAndRedraw()
		return true
	}
	ctx.ConsumeAndRedraw()
	return true
}

func (o *Overlays) drawExtConfirm(ctx components.DrawContext, width, height int) components.Surface {
	st := o.confirm
	if st == nil {
		return components.NewSurface(width, height, nil)
	}
	th := o.theme
	if width <= 0 {
		width = 80
	}
	innerW := width - 4
	if innerW < 10 {
		innerW = width
	}
	if height <= 0 {
		height = st.preferredAskHeight(width, ctx.Method)
	}

	border := chrome.ModalBorder(th)
	if st.danger {
		border = th.Destructive
	}
	return paintAskPanel(st.bodyLines(th, innerW, ctx.Method), width, height, border, ctx.Method)
}
