package configui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/layout"
)

// Row geometry. Every column is fixed so the eye can run down one line of
// labels and one line of values.
const (
	xSection    = 2
	xModel      = 4
	xField      = 4
	xModelField = 6
	xNested     = 8
)

// valueCol is the shared value column; it shrinks on narrow terminals.
func (e *ConfigEditor) valueCol() int {
	col := 28
	if e.width > 0 {
		col = min(col, max(e.width/3, 16))
	}
	return col
}

// Draw paints the frame the App loop shows.
func (e *ConfigEditor) Draw(ctx components.DrawContext) components.Surface {
	w, h := ctx.Max.Width, ctx.Max.Height
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	e.width = w
	e.pollFetch()
	e.rebuild()

	th := e.themeOrDefault()
	out := components.NewSurface(w, h, e)
	layout.DrawRoundedBorder(&out, layout.BorderRounded, th.Border,
		e.topLabel(), nil, nil, nil, ctx.Method)

	inner := max(h-2, 1)
	footer := 2
	if inner < 5 {
		footer = 0
	}
	page := max(inner-footer, 1)
	e.page = page
	e.rowsTop = 1
	e.ensureVisible(page)

	for i := e.scroll; i < len(e.rows) && i < e.scroll+page; i++ {
		e.paintRow(&out, 1+i-e.scroll, e.rows[i], i == e.cursor, ctx.Method)
	}
	if footer > 0 {
		e.paintFooter(&out, ctx.Method)
	}
	if e.edit != nil {
		out.Cursor = &components.Point{X: e.caretX, Y: e.caretY}
	}

	if e.confirm != nil {
		out.Children = append(out.Children, e.drawConfirm(w, h, ctx.Method))
	}
	if e.picker.Open {
		out.Children = append(out.Children, components.SubSurface{
			Z:       20,
			Surface: e.picker.Draw(ctx),
		})
	}
	return out
}

// topLabel is the title bar: what this screen is, then the file it writes,
// then an unsaved marker. The title comes first so a long path is what gets
// truncated on a narrow terminal.
func (e *ConfigEditor) topLabel() *layout.BorderLabel {
	th := e.themeOrDefault()
	spans := []layout.BorderSpan{
		{Text: " phi config ", Style: chrome.PanelTitle(th)},
		{Text: "· " + displayPath(e.path) + " ", Style: th.Muted},
	}
	if e.dirty {
		spans = append(spans, layout.BorderSpan{Text: "● ", Style: th.Warning})
	}
	return &layout.BorderLabel{Spans: spans}
}

func (e *ConfigEditor) ensureVisible(page int) {
	if len(e.rows) == 0 {
		e.cursor, e.scroll = 0, 0
		return
	}
	e.cursor = min(max(e.cursor, 0), len(e.rows)-1)
	if e.cursor < e.scroll {
		e.scroll = e.cursor
	}
	if e.cursor >= e.scroll+page {
		e.scroll = e.cursor - page + 1
	}
	e.scroll = min(max(e.scroll, 0), max(len(e.rows)-page, 0))
}

func (e *ConfigEditor) paintRow(s *components.Surface, y int, r row, selected bool, method xui.WidthMethod) {
	th := e.themeOrDefault()
	base, secondary := th.Foreground, secondaryStyle(th)
	bg := th.SelectionBg.Bg
	if selected {
		for x := 1; x < s.Size.Width-1; x++ {
			s.SetCell(x, y, xui.Cell{Char: " ", Width: 1, Style: xui.Style{Bg: bg}})
		}
		base = th.SelectionFg
		base.Bg = bg
		// Text on the stripe takes the stripe's tone, but the label column stays
		// one weight below the value column: the eye belongs on the data, not on
		// the key names.
		secondary = base
		secondary.Bold = false
	}
	switch r.kind {
	case rowSection:
		e.paintSection(s, y, r, method)
	case rowModel:
		// A model header is a label/value row like any other: the name owns the
		// label column and the default marker plus the preset summary share the
		// value column, so a long name can never overwrite the badge.
		col := e.valueCol()
		nameStyle := base
		nameStyle.Bold = true
		s.Print(xModel, y, layout.TruncateToWidth(r.name, max(col-1-xModel, 1), method), nameStyle, method)
		x := col
		if r.badge != "" {
			x += s.Print(x, y, r.badge, withBG(th.Success, bg, selected), method)
			if r.note != "" {
				x += s.Print(x, y, chrome.Sep, secondary, method)
			}
		}
		if r.note != "" {
			s.Print(x, y, layout.TruncateToWidth(r.note, max(s.Size.Width-2-x, 1), method), secondary, method)
		}
	case rowItem:
		x := xNested
		x += s.Print(x, y, "· ", withBG(th.Border, bg, selected), method)
		style := e.valStyle(r.style, bg, selected)
		text := r.label
		if e.edit != nil && e.edit.key == r.key {
			text = string(e.edit.buf)
			style = base
			if selected {
				style.Bg = bg
			}
		}
		s.Print(x, y, layout.TruncateToWidth(text, s.Size.Width-2-x, method), style, method)
		if e.edit != nil && e.edit.key == r.key {
			e.caretX = x + xui.StringWidth(string(e.edit.buf[:e.edit.cur]), method)
			e.caretY = y
		}
	case rowAdd:
		style := th.ToolName
		if selected {
			style = base
		}
		s.Print(xNested, y, layout.TruncateToWidth(r.label, s.Size.Width-2-xNested, method), style, method)
	case rowField:
		e.paintField(s, y, r, selected, base, secondary, bg, method)
	}
}

func (e *ConfigEditor) paintField(
	s *components.Surface,
	y int,
	r row,
	selected bool,
	base, secondary xui.Style,
	bg xui.Color,
	method xui.WidthMethod,
) {
	th := e.themeOrDefault()
	x := xField
	if _, _, ok := splitModelKey(r.key); ok {
		x = xModelField
	}
	col := e.valueCol()
	// Labels are a reading aid: they never take emphasis away from the value
	// column, on or off the stripe.
	labelStyle := secondary
	s.Print(x, y, layout.TruncateToWidth(r.label, max(col-x-2, 1), method), labelStyle, method)

	style := e.valStyle(r.style, bg, selected)
	text := r.display
	editing := e.edit != nil && e.edit.key == r.key
	if editing {
		text = string(e.edit.buf)
		style = base
		if selected {
			style.Bg = bg
		}
	}
	avail := max(s.Size.Width-2-col, 1)
	printed := s.Print(col, y, layout.TruncateToWidth(text, avail, method), style, method)
	if editing {
		e.caretX = col + xui.StringWidth(string(e.edit.buf[:e.edit.cur]), method)
		e.caretY = y
		return
	}
	if selected && r.hint != "" {
		note := th.Muted
		note.Dim = true
		// The selection bar is one continuous stripe, hint included.
		note.Bg = bg
		s.Print(col+printed+2, y, layout.TruncateToWidth(r.hint, max(avail-printed-2, 0), method), note, method)
	}
}

func (e *ConfigEditor) paintSection(s *components.Surface, y int, r row, method xui.WidthMethod) {
	th := e.themeOrDefault()
	title := th.TitleOrForeground()
	x := xSection
	x += s.Print(x, y, strings.ToUpper(r.title), title, method)

	end := s.Size.Width - 2
	note := r.note
	noteW := 0
	if note != "" {
		noteW = xui.StringWidth(note, method) + 1
		// Drop the note rather than let it crowd out the rule on a narrow frame.
		if end-x-1-noteW < 2 {
			note, noteW = "", 0
		}
	}
	if rule := end - x - 1 - noteW; rule > 0 {
		s.Print(x+1, y, strings.Repeat("─", rule), th.Border, method)
	}
	if note != "" {
		s.Print(end-noteW+1, y, note, th.Muted, method)
	}
}

func (e *ConfigEditor) paintFooter(s *components.Surface, method xui.WidthMethod) {
	th := e.themeOrDefault()
	w, h := s.Size.Width, s.Size.Height
	ruleY := h - 3
	if ruleY > 1 {
		s.Print(1, ruleY, strings.Repeat("─", max(w-2, 0)), th.Border, method)
	}
	y := h - 2
	// hintLine keeps the line inside the width on its own; the truncation is
	// only there for the one-hint terminal, where nothing can be dropped.
	hints := e.hintLine(max(w-6, 1), method)
	printed := s.Print(2, y, layout.TruncateToWidth(hints, max(w-6, 1), method), th.Muted, method)
	if e.status == "" {
		return
	}
	statusW := xui.StringWidth(e.status, method)
	start := w - 2 - statusW
	if start <= 4+printed {
		return
	}
	s.Print(start, y, layout.TruncateToWidth(e.status, max(w-4, 1), method), e.statusStyle(), method)
}

// hintSegments lists the current state's shortcuts, most useful first. The
// form has more keys than an 80-column footer holds, so the tail is what gets
// dropped — see hintLine.
func (e *ConfigEditor) hintSegments() []string {
	switch {
	case e.edit != nil:
		return []string{"⏎ apply", "esc cancel", "^u clear"}
	case e.confirm != nil:
		// The shared decision-panel line reads as one hint.
		return []string{chrome.ConfirmHint()}
	case e.expanded != "":
		return []string{"↑↓ move", "⏎ edit", "a add", "d delete", "esc close"}
	default:
		// Save outranks fetch: nothing reaches the file until it runs.
		return []string{
			"↑↓ move", "⏎ edit", "←→ cycle", "a add model", "d delete", "s save", "f fetch", "q quit",
		}
	}
}

// hintLine joins as many hints as maxWidth holds. A hint is shown whole or not
// at all: dropping the last one reads better than the half word a plain
// truncation leaves behind.
func (e *ConfigEditor) hintLine(maxWidth int, method xui.WidthMethod) string {
	line := ""
	for _, seg := range e.hintSegments() {
		next := seg
		if line != "" {
			next = line + chrome.Sep + seg
		}
		if xui.StringWidth(next, method) > maxWidth {
			if line == "" {
				return seg
			}
			break
		}
		line = next
	}
	return line
}

func (e *ConfigEditor) statusStyle() xui.Style {
	th := e.themeOrDefault()
	switch e.statusKind {
	case statusError:
		return th.Destructive
	case statusSuccess:
		return th.Success
	default:
		return th.Muted
	}
}

// secondaryStyle is the form's one quiet tone: field labels, values the file
// does not set, and rules inherited from the built-in preset all share it.
//
// Dim is cleared on purpose. SGR 2 lands anywhere between "slightly softer" and
// "all but invisible" depending on the terminal, and a label column that sinks
// into the background next to a crisp value column is what makes a two-column
// form read as mud. Trailing hints keep Dim — they are meant to disappear first.
func secondaryStyle(th components.Theme) xui.Style {
	st := th.Muted
	st.Dim = false
	return st
}

func (e *ConfigEditor) valStyle(v valueStyle, bg xui.Color, selected bool) xui.Style {
	th := e.themeOrDefault()
	var st xui.Style
	switch v {
	case styleMuted:
		st = secondaryStyle(th)
	case styleOn:
		st = th.Success
	case styleWarn:
		st = th.Warning
	default:
		st = th.Foreground
	}
	if selected {
		// A filled value takes the stripe highlight; an unset one keeps the flat
		// quiet tone it has everywhere else, so the cursor cannot make an empty
		// field look written.
		if v == styleValue {
			st = th.SelectionFg
		}
		st.Bg = bg
		st.Dim = false
	}
	return st
}

type statusKind int

const (
	statusNone statusKind = iota
	statusInfo
	statusSuccess
	statusError
)

func withBG(st xui.Style, bg xui.Color, selected bool) xui.Style {
	if selected {
		st.Bg = bg
	}
	return st
}

// drawConfirm paints the yes/no modal centered over the form.
func (e *ConfigEditor) drawConfirm(w, h int, method xui.WidthMethod) components.SubSurface {
	th := e.themeOrDefault()
	st := e.confirm

	width := min(58, max(w-4, 20))
	bodyW := width - 4
	var lines []components.RichLine
	add := func(spans ...components.Span) {
		lines = append(lines, components.WrapSpans(spans, bodyW, method)...)
	}
	add(components.Span{Text: st.title, Style: th.TitleOrForeground()})
	if st.body != "" {
		add(components.Span{Text: st.body, Style: th.Muted})
	}
	lines = append(lines, components.RichLine{})

	primary := chrome.DecisionPrimary(th)
	border := chrome.ModalBorder(th)
	if st.danger {
		border = th.Destructive
	}
	for i, label := range []string{"Yes", "No"} {
		lines = append(lines, chrome.OptionLine(th, primary, label, (i == 0) == st.yes, bodyW, method)...)
	}
	add(components.Span{Text: chrome.ConfirmHint(), Style: th.Muted})

	height := len(lines) + 2
	panel := components.NewSurface(width, height, e)
	fill := xui.Style{Fg: th.Foreground.Fg}
	for y := range height {
		for x := range width {
			panel.SetCell(x, y, xui.Cell{Char: " ", Width: 1, Style: fill})
		}
	}
	layout.DrawRoundedBorder(&panel, layout.BorderRounded, border, nil, nil, nil, nil, method)
	y := 1
	for _, line := range lines {
		if y >= height-1 {
			break
		}
		components.PaintSpans(&panel, 2, y, line, method)
		y++
	}
	return components.SubSurface{
		Origin:  components.Point{X: max((w-width)/2, 0), Y: max((h-height)/2, 0)},
		Z:       30,
		Surface: panel,
	}
}

// displayPath shortens the home directory to "~" for the border label. Slashes
// are normalized so Windows shows "~/.phi/config.yaml" instead of "~\.phi\config.yaml".
func displayPath(path string) string {
	display := filepath.ToSlash(path)
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rest, ok := strings.CutPrefix(display, filepath.ToSlash(home)); ok {
			return "~" + rest
		}
	}
	return display
}
