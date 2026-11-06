package footer

import (
	"strings"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/layout"
	"github.com/pulseaiclub/phi/internal/session"
)

// FooterChrome only owns drawing configuration. Runtime status is assembled by
// the editor from controller and transcript state and passed to Draw.
type FooterChrome struct {
	theme components.Theme
}

// State contains the ambient text rendered on the bottom footer row.
type State struct {
	ExtensionStatus string
	UpdateHint      string
}

// StatusInput contains the domain state needed for the composer's status slot.
type StatusInput struct {
	Activity      string
	ShowSpinner   bool
	Spinner       flowSpinner
	Usage         session.TokenUsage
	ContextWindow int
}

type flowSpinner interface {
	ForEachFlowCell(string, func(string, bool))
}

// NewFooterChrome builds footer drawing configuration.
func NewFooterChrome(theme components.Theme) *FooterChrome {
	return &FooterChrome{theme: theme}
}

// SetTheme updates footer drawing styles.
func (f *FooterChrome) SetTheme(th components.Theme) {
	f.theme = th
}

// StatusLabel derives the composer's ambient status label from current state.
func StatusLabel(th components.Theme, input StatusInput) layout.BorderLabel {
	if input.Activity != "" {
		return activityStatusLabel(th, input.ShowSpinner, input.Spinner, input.Activity)
	}
	return tokenStatusLabel(th, input.Usage, input.ContextWindow)
}

func activityStatusLabel(th components.Theme, showSpinner bool, spin flowSpinner, msg string) layout.BorderLabel {
	dim := ChromeLabelStyle(th)
	if !showSpinner || spin == nil {
		return layout.BorderLabel{Text: msg, Style: dim}
	}
	on := th.Foreground
	spans := make([]layout.BorderSpan, 0, len(msg)+1)
	spin.ForEachFlowCell(msg, func(ch string, lit bool) {
		st := dim
		if lit {
			st = on
		}
		spans = append(spans, layout.BorderSpan{Text: ch, Style: st})
	})
	return layout.BorderLabel{Spans: spans}
}

// Draw renders the bottom footer row.
func (f *FooterChrome) Draw(ctx components.DrawContext, width int, state State) components.Surface {
	footer := components.NewSurface(width, 1, nil)
	dim := f.theme.Muted
	msg := strings.TrimSpace(state.ExtensionStatus)

	x := 1
	if msg != "" {
		footer.Print(x, 0, msg, dim, ctx.Method)
		x += xui.StringWidth(msg, ctx.Method)
	}

	hint := strings.TrimSpace(state.UpdateHint)
	if hint != "" {
		hw := xui.StringWidth(hint, ctx.Method)
		hx := width - hw - 1
		hx = max(hx, x+2)
		if hx+hw <= width {
			st := f.theme.TitleOrForeground()
			st.Bold = false
			footer.Print(hx, 0, hint, st, ctx.Method)
		}
	}
	return footer
}
