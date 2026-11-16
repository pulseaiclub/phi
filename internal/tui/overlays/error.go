package overlays

import (
	"strings"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

var errorOptionLabels = []string{
	"Retry",
	"Copy to Clipboard",
	"Dismiss",
}

const (
	errOptRetry = iota
	errOptCopy
	errOptDismiss
)

type errorAskState struct {
	message  string
	selected int
}

func errorInnerWidth(width int) int {
	return max(width-3, 0)
}

func (st *errorAskState) wrappedLines(
	th components.Theme,
	innerW int,
	method xui.WidthMethod,
) (title, message []components.RichLine, options [][]components.RichLine, hint []components.RichLine) {
	wrap := func(spans ...components.Span) []components.RichLine {
		return components.WrapSpans(spans, max(innerW, 1), method)
	}

	title = wrap(components.Span{Text: "Error", Style: xui.Style{Bold: true, Fg: th.Destructive.Fg}})
	if st.message != "" {
		for line := range strings.SplitSeq(st.message, "\n") {
			message = append(message, wrap(components.Span{Text: line, Style: th.Foreground})...)
		}
	}
	for i, label := range errorOptionLabels {
		options = append(
			options,
			chrome.OptionLine(th, th.Destructive, label, i == st.selected, max(innerW, 1), method),
		)
	}
	hint = wrap(components.Span{
		Text:  chrome.AskHint("↑↓ move", "select", "dismiss"),
		Style: th.Muted,
	})
	return title, message, options, hint
}

func (st *errorAskState) preferredAskHeight(width int, method xui.WidthMethod) int {
	if width <= 0 {
		width = 80
	}
	innerW := errorInnerWidth(width)
	title, message, options, hint := st.wrappedLines(components.DefaultTheme(), innerW, method)
	bodyLines := len(title) + len(message) + len(hint)
	for _, lines := range options {
		bodyLines += len(lines)
	}
	if len(message) > 0 {
		bodyLines += 2
	} else {
		bodyLines++
	}
	return max(2+bodyLines, 8)
}

func flattenErrorOptions(options [][]components.RichLine) []components.RichLine {
	var lines []components.RichLine
	for _, option := range options {
		lines = append(lines, option...)
	}
	return lines
}

func errorOptionViewport(
	all []components.RichLine,
	options [][]components.RichLine,
	selected, height int,
) []components.RichLine {
	if height <= 0 || len(all) <= height {
		return all
	}
	selected = min(max(selected, 0), len(options)-1)
	selectedStart := 0
	for i := 0; i < selected; i++ {
		selectedStart += len(options[i])
	}
	selectedEnd := selectedStart + len(options[selected])
	if selectedEnd-selectedStart >= height {
		return options[selected][:height]
	}
	start := selectedStart - (height-(selectedEnd-selectedStart))/2
	start = min(max(start, 0), len(all)-height)
	return all[start : start+height]
}

func truncateErrorMessage(lines []components.RichLine, height int, muted xui.Style) []components.RichLine {
	if height <= 0 {
		return nil
	}
	if len(lines) <= height {
		return lines
	}
	lines = lines[:height]
	lines[height-1] = components.RichLine{{Text: "…", Style: muted}}
	return lines
}

func (st *errorAskState) visibleLines(
	th components.Theme,
	innerW, height int,
	method xui.WidthMethod,
) []components.RichLine {
	if innerW < 1 || height <= 2 {
		return nil
	}
	title, message, options, hint := st.wrappedLines(th, innerW, method)
	actions := flattenErrorOptions(options)
	available := height - 2
	full := make([]components.RichLine, 0, available)
	full = append(full, title...)
	if len(message) > 0 {
		full = append(full, components.RichLine{})
		full = append(full, message...)
	}
	full = append(full, components.RichLine{})
	full = append(full, actions...)
	full = append(full, hint...)
	if len(full) <= available {
		return full
	}

	if len(title)+len(actions) <= available {
		lines := make([]components.RichLine, 0, available)
		lines = append(lines, title...)
		remaining := available - len(title) - len(actions)
		showHint := len(hint) <= remaining
		if showHint {
			remaining -= len(hint)
		}
		lines = append(lines, truncateErrorMessage(message, remaining, th.Muted)...)
		lines = append(lines, actions...)
		if showHint {
			lines = append(lines, hint...)
		}
		return lines
	}

	return errorOptionViewport(actions, options, st.selected, available)
}

func (o *Overlays) beginErrorAsk(msg controller.OverlayMsg) {
	if o.err != nil {
		o.resolveError(controller.ErrorReply{})
	}
	if o.perm != nil {
		o.resolvePermission(controller.AskReply{})
	}
	if o.confirm != nil {
		o.resolveExtConfirm(controller.ExtConfirmReply{})
	}
	if o.composer != nil {
		o.composer.HideCompleters()
		o.composer.HidePalette()
	}
	o.err = &errorAskState{message: msg.ErrorText}
	// The turn is over — this dialog is the aftermath, not an in-flight ask.
	o.activity.Apply(controller.ActivityIdle)
	if o.focusEditor != nil {
		o.focusEditor()
	}
}

func (o *Overlays) dismissError() {
	was := o.err != nil
	o.err = nil
	if !was {
		return
	}
	if o.activity != nil && o.activity.Current == controller.ActivityAwaitingApproval {
		o.activity.Apply(controller.ActivityIdle)
	}
	if o.focusChat != nil {
		o.focusChat()
	}
}

// resolveError clears the overlay and delivers the reply directly to the
// onResolve callback: nobody waits on this dialog, so unlike the ask
// overlays there is no reply chan.
func (o *Overlays) resolveError(r controller.ErrorReply) {
	st := o.err
	if st == nil {
		return
	}
	text := st.message
	o.dismissError()
	if o.onErrorResolve != nil {
		o.onErrorResolve(r, text)
	}
}

// ResolveError sends an error-dialog reply and clears the overlay.
func (o *Overlays) ResolveError(r controller.ErrorReply) {
	o.resolveError(r)
}

func (o *Overlays) handleErrorKey(ctx *components.EventContext, e xui.KeyEvent) bool {
	st := o.err
	if st == nil || !e.Press {
		return false
	}
	switch e.Code {
	case xui.KeyEscape:
		o.resolveError(controller.ErrorReply{})
	case xui.KeyUp, xui.KeyLeft:
		if st.selected > 0 {
			st.selected--
		} else {
			st.selected = len(errorOptionLabels) - 1
		}
	case xui.KeyDown, xui.KeyRight, xui.KeyTab:
		st.selected = (st.selected + 1) % len(errorOptionLabels)
	case xui.KeyEnter:
		o.acceptErrorOption(st.selected)
	case xui.KeyRune:
		if e.Mods.Has(xui.ModCtrl) || e.Mods.Has(xui.ModAlt) {
			break
		}
		switch e.Rune {
		case 'r', 'R':
			o.acceptErrorOption(errOptRetry)
		case 'c', 'C', 'y', 'Y':
			o.acceptErrorOption(errOptCopy)
		case 'd', 'D', 'n', 'N':
			o.acceptErrorOption(errOptDismiss)
		case 'k', 'K', 'h', 'H':
			if st.selected > 0 {
				st.selected--
			}
		case 'j', 'J', 'l', 'L':
			st.selected = (st.selected + 1) % len(errorOptionLabels)
		}
	}
	ctx.ConsumeAndRedraw()
	return true
}

func (o *Overlays) acceptErrorOption(idx int) {
	switch idx {
	case errOptRetry:
		o.resolveError(controller.ErrorReply{Retry: true})
	case errOptCopy:
		o.resolveError(controller.ErrorReply{Copy: true})
	default:
		o.resolveError(controller.ErrorReply{})
	}
}

func (o *Overlays) drawError(ctx components.DrawContext, width, height int) components.Surface {
	st := o.err
	if st == nil {
		return components.NewSurface(width, height, nil)
	}
	th := o.theme
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = st.preferredAskHeight(width, ctx.Method)
	}
	body := st.visibleLines(th, errorInnerWidth(width), height, ctx.Method)
	return paintAskPanel(body, width, height, th.Destructive, ctx.Method)
}
