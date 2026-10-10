// Package chrome locks TUI visual grammar: glyphs, hints, borders, and
// decision-row chrome. Call sites should not invent parallel dialects.
//
// Color roles live on components.Theme; this package only picks the right
// role for a job (ModalBorder, PanelTitle, DecisionPrimary, ToolIcon, …).
package chrome

import (
	"strings"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/status"
)

// Status / expand glyphs.
const (
	Ok       = "✓"
	Err      = "✗"
	Stop     = "⊘"
	Busy     = "⋯"
	Expand   = " ▶"
	Collapse = " ▼"
)

// Decision-row glyphs (permission / continue / confirm).
const (
	SelectArrow = "▸"
	DotOn       = "●"
	DotOff      = "○"
)

// Prompt glyphs.
const (
	FilterPrompt = ">"
	SoftPrompt   = "› "
	BashPrompt   = "$ "
)

// CurrentMarker is the leading "you are here" column for a list of choices:
// the branch-picker dot when current, else a blank of equal width so labels
// stay aligned. One glyph for the whole product — any list that marks a
// current item uses this, never its own checkmark.
func CurrentMarker(current bool) string {
	if current {
		return DotOn + " "
	}
	return "  "
}

// Sep joins hint fragments. One separator for the whole product.
const Sep = " · "

// ListHint is the default bottom-border caption for filterable list overlays.
func ListHint(action string) string {
	if action == "" {
		action = "select"
	}
	return ListHintExtra(action)
}

// ListHintExtra is ListHint with extra key fragments spliced before "esc close".
// Fragments keep the same "key verb" shape as the built-in ones, so domains
// extend the caption instead of restating it.
func ListHintExtra(action string, extra ...string) string {
	parts := make([]string, 0, 3+len(extra))
	parts = append(parts, " ↑↓ move", "⏎ "+action)
	parts = append(parts, extra...)
	parts = append(parts, "esc close ")
	return strings.Join(parts, Sep)
}

// ListHintShort is the compact fallback when the full ListHint does not fit.
func ListHintShort(action string) string {
	if action == "" {
		action = "select"
	}
	return " ⏎ " + action + Sep + "esc close "
}

// AskHint builds muted footer copy for decision panels.
// nav is typically "↑↓ move" or "←→ move"; action/dismiss are verb phrases
// after the key (e.g. "select", "cancel").
func AskHint(nav, action, dismiss string) string {
	parts := make([]string, 0, 3)
	if nav != "" {
		parts = append(parts, nav)
	}
	if action != "" {
		parts = append(parts, "Enter "+action)
	}
	if dismiss != "" {
		parts = append(parts, "Esc "+dismiss)
	}
	return strings.Join(parts, Sep)
}

// ConfirmHint is the confirm-panel footer (includes Y/N chords). Decision rows
// stack vertically, so the nav fragment names the vertical axis.
func ConfirmHint() string {
	return "↑↓ move" + Sep + "Enter confirm" + Sep + "Y yes" + Sep + "N/Esc cancel"
}

// FeedbackHint is the deny-with-feedback footer.
func FeedbackHint() string {
	return "Enter send" + Sep + "Esc cancel"
}

// DecisionPrimary is the cool action accent for selected decision rows.
func DecisionPrimary(th components.Theme) xui.Style {
	if th.ToolName.Fg.Kind != 0 {
		return th.ToolName
	}
	return th.Success
}

// ModalBorder is the elevated border for ask/confirm panels (structure, not alarm).
func ModalBorder(th components.Theme) xui.Style {
	if th.Title.Fg.Kind != 0 {
		return th.Title
	}
	return th.Border
}

// PanelTitle styles overlay / palette titles.
func PanelTitle(th components.Theme) xui.Style {
	return th.TitleOrForeground()
}

// OptionLine paints one ▸● /  ○ decision row.
func OptionLine(
	th components.Theme,
	primary xui.Style,
	label string,
	selected bool,
	innerW int,
	method xui.WidthMethod,
) []components.RichLine {
	arrow, dot := " ", DotOff
	labelSt, dotSt := th.Foreground, th.Muted
	if selected {
		arrow, dot = SelectArrow, DotOn
		labelSt = xui.Style{Bold: true, Fg: primary.Fg}
		dotSt = primary
	}
	return components.WrapSpans([]components.Span{
		{Text: arrow, Style: primary},
		{Text: dot, Style: dotSt},
		{Text: " " + label, Style: labelSt},
	}, innerW, method)
}

// ExpandArrow returns the collapsed/expanded disclosure glyph.
func ExpandArrow(expanded bool) string {
	if expanded {
		return Collapse
	}
	return Expand
}

// ToolIcon returns the status glyph + style for a tool / agent title row.
func ToolIcon(st status.ToolStatus, th components.Theme, spin *status.Spinner) (string, xui.Style) {
	icon := Ok
	iconSt := th.Success
	switch st {
	case status.ToolRunning, status.ToolQueued:
		icon = Busy
		iconSt = th.ToolName
		if spin != nil {
			icon = spin.Glyph()
		}
	case status.ToolError:
		icon = Err
		iconSt = th.Destructive
	case status.ToolCancelled:
		icon = Stop
		iconSt = th.Muted
	case status.ToolRejected:
		icon = Stop
		iconSt = th.Destructive
	}
	return icon, iconSt
}

// ChildIcon is the compact nested-tree status mark (no spinner).
func ChildIcon(st status.ToolStatus) string {
	switch st {
	case status.ToolRunning, status.ToolQueued:
		return Busy
	case status.ToolError:
		return Err
	case status.ToolCancelled, status.ToolRejected:
		return Stop
	default:
		return Ok
	}
}

// StatusSuffix is the muted parenthetical for cancelled / rejected rows.
func StatusSuffix(st status.ToolStatus) string {
	switch st {
	case status.ToolCancelled:
		return " (cancelled)"
	case status.ToolRejected:
		return " (rejected)"
	default:
		return ""
	}
}
