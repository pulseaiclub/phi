// Package ask provides the modal ask panel: a titled question with an
// optional message and keyboard-selectable options. Dialog owns rendering
// and key mechanics only; lifecycle, reply mapping, and focus effects stay
// with the caller.
package ask

import (
	"strings"
	"unicode"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
)

// Option is one selectable row. Keys lists runes that confirm the option
// directly, matched case-insensitively — "cy" accepts either C or Y.
type Option struct {
	Label string
	Keys  string
}

// Dialog is a dumb ask panel: set the fields and it is ready to draw, with
// the first option selected. HandleKey and Draw are its whole behavior.
type Dialog struct {
	Title    string
	Message  string
	Hint     string
	Options  []Option
	selected int
}

// HandleKey applies one key press. It reports chosen=true when the user
// commits — Enter, an option hotkey, or Esc — and pick is the confirmed
// option index, or -1 for Esc. Otherwise the selection may have moved and
// the caller redraws; the dialog consumes every key.
func (d *Dialog) HandleKey(e xui.KeyEvent) (pick int, chosen bool) {
	if len(d.Options) == 0 {
		return 0, false
	}
	switch e.Code {
	case xui.KeyEscape:
		return -1, true
	case xui.KeyUp, xui.KeyLeft:
		if d.selected > 0 {
			d.selected--
		} else {
			d.selected = len(d.Options) - 1
		}
	case xui.KeyDown, xui.KeyRight, xui.KeyTab:
		d.selected = (d.selected + 1) % len(d.Options)
	case xui.KeyEnter:
		return d.selected, true
	case xui.KeyRune:
		if e.Mods.Has(xui.ModCtrl) || e.Mods.Has(xui.ModAlt) {
			break
		}
		if i, ok := d.hotkey(e.Rune); ok {
			return i, true
		}
		switch unicode.ToLower(e.Rune) {
		case 'k', 'h':
			if d.selected > 0 {
				d.selected--
			}
		case 'j', 'l':
			d.selected = (d.selected + 1) % len(d.Options)
		}
	}
	return 0, false
}

// hotkey returns the option whose Keys contain r.
func (d *Dialog) hotkey(r rune) (int, bool) {
	r = unicode.ToLower(r)
	for i, opt := range d.Options {
		if strings.ContainsRune(strings.ToLower(opt.Keys), r) {
			return i, true
		}
	}
	return 0, false
}

// PreferredHeight reports the panel's natural height at the given width.
func (d *Dialog) PreferredHeight(width int, method xui.WidthMethod) int {
	if width <= 0 {
		width = 80
	}
	title, message, options, hint := d.lines(components.DefaultTheme(), xui.Style{}, innerWidth(width), method)
	body := len(title) + len(message) + len(hint)
	for _, rows := range options {
		body += len(rows)
	}
	if len(message) > 0 {
		body += 2
	} else {
		body++
	}
	return max(2+body, 8)
}

// Draw paints the panel; accent colors the border, title, and selected
// option. Non-positive width or height falls back to 80 / the preferred
// height.
func (d *Dialog) Draw(
	th components.Theme,
	accent xui.Style,
	width, height int,
	method xui.WidthMethod,
) components.Surface {
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = d.PreferredHeight(width, method)
	}
	body := d.visibleLines(th, accent, innerWidth(width), height, method)
	return chrome.AskPanel(body, width, height, accent, method)
}

func innerWidth(width int) int {
	return max(width-3, 0)
}

// lines wraps the panel content to innerW: bold-accent title, plain message,
// one decision row per option, muted hint.
func (d *Dialog) lines(
	th components.Theme,
	accent xui.Style,
	innerW int,
	method xui.WidthMethod,
) (title, message []components.RichLine, options [][]components.RichLine, hint []components.RichLine) {
	wrap := func(spans ...components.Span) []components.RichLine {
		return components.WrapSpans(spans, max(innerW, 1), method)
	}
	title = wrap(components.Span{Text: d.Title, Style: xui.Style{Bold: true, Fg: accent.Fg}})
	if d.Message != "" {
		for line := range strings.SplitSeq(d.Message, "\n") {
			message = append(message, wrap(components.Span{Text: line, Style: th.Foreground})...)
		}
	}
	for i, opt := range d.Options {
		options = append(
			options,
			chrome.OptionLine(th, accent, opt.Label, i == d.selected, max(innerW, 1), method),
		)
	}
	hint = wrap(components.Span{Text: d.Hint, Style: th.Muted})
	return title, message, options, hint
}

func (d *Dialog) visibleLines(
	th components.Theme,
	accent xui.Style,
	innerW, height int,
	method xui.WidthMethod,
) []components.RichLine {
	if innerW < 1 || height <= 2 {
		return nil
	}
	title, message, options, hint := d.lines(th, accent, innerW, method)
	actions := flattenOptions(options)
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
		lines = append(lines, truncateLines(message, remaining, th.Muted)...)
		lines = append(lines, actions...)
		if showHint {
			lines = append(lines, hint...)
		}
		return lines
	}

	return optionViewport(actions, options, d.selected, available)
}

func flattenOptions(options [][]components.RichLine) []components.RichLine {
	var lines []components.RichLine
	for _, option := range options {
		lines = append(lines, option...)
	}
	return lines
}

// optionViewport centers the option window on the selected row when even the
// actions cannot fit.
func optionViewport(
	all []components.RichLine,
	options [][]components.RichLine,
	selected, height int,
) []components.RichLine {
	if height <= 0 || len(all) <= height {
		return all
	}
	selected = min(max(selected, 0), len(options)-1)
	selectedStart := 0
	for i := range selected {
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

func truncateLines(lines []components.RichLine, height int, muted xui.Style) []components.RichLine {
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
