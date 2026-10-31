package codeview

import (
	"strconv"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
)

// Model is everything the painter needs for one frame of the file viewer.
// Mirror of diffview.Model with a single indexed document instead of diff rows.
type Model struct {
	Theme      components.Theme
	Title      string                    // e.g. "src/app.go"
	Status     string                    // left status text, e.g. "src/app.go:3:1 · 214 lines"
	Hint       string                    // key hint line, e.g. "esc close · j/k move"
	Path       string                    // file path used for syntax detection (lexers.Match)
	Lines      []string                  // raw file lines without trailing newline
	Highlight  map[int][]components.Span // by line index; nil = plain
	CursorLine int                       // 0-based line index of the cursor
	CursorCol  int                       // 0-based display column
	Scroll     int                       // first visible line index
	XScroll    int                       // horizontal column offset
	ViewH      int                       // body height in rows (0 = derive from ctx.Max.Height-2)
	Selecting  bool
	SelStart   components.Point // anchor (Y = line index, X = display col)
	SelEnd     components.Point
	Empty      string // shown when Lines is empty
}

const (
	defaultTitle = "file"
	defaultHint  = "esc close" + chrome.Sep + "j/k move" + chrome.Sep + "gg/G top/bottom"
	// gutterRule separates the right-aligned line number from the code. The
	// width is a separate constant: len(gutterRule) counts bytes, not cells.
	gutterRule      = " │ "
	gutterRuleWidth = 3
)

// Paint draws the full-screen file view.
func Paint(ctx components.DrawContext, m Model) components.Surface {
	w, h := ctx.Max.Width, ctx.Max.Height
	if w < 1 {
		w = 80
	}
	if h < 1 {
		h = 24
	}
	s := components.NewSurface(w, h, nil)
	th := m.Theme
	method := ctx.Method

	fillRow(&s, 0, w, th.Title)
	title := m.Title
	if title == "" {
		title = m.Path
	}
	if title == "" {
		title = defaultTitle
	}
	s.Print(1, 0, components.TruncateToWidth(title, w-2, method), th.TitleOrForeground(), method)

	numW := lineNumberWidth(len(m.Lines))
	gutterW := numW + gutterRuleWidth
	bodyH := m.ViewH
	if bodyH <= 0 {
		bodyH = h - 2
	}
	bodyH = max(min(bodyH, h-2), 1)
	scroll := clampScroll(m.Scroll, len(m.Lines))

	if len(m.Lines) == 0 {
		if m.Empty != "" {
			s.Print(1, 2, components.TruncateToWidth(m.Empty, w-2, method), th.Muted, method)
		}
	} else {
		y := 1
		for line := scroll; line < len(m.Lines) && y < 1+bodyH && y < h-1; line, y = line+1, y+1 {
			paintLine(&s, y, w, m, line, gutterW, numW, method)
		}
	}

	paintStatus(&s, w, h, m, method)

	if m.Selecting {
		paintSelection(&s, m, scroll, gutterW, bodyH)
	}
	if len(m.Lines) > 0 {
		paintCursor(&s, m, w, scroll, gutterW, bodyH)
	}
	return s
}

func paintLine(s *components.Surface, y, w int, m Model, line, gutterW, numW int, method xui.WidthMethod) {
	th := m.Theme
	base := rowStyle(th, line == m.CursorLine)
	fillRow(s, y, w, base)
	paintGutter(s, y, numW, line+1, th, base, method)
	codeW := w - gutterW
	if codeW < 1 {
		return
	}
	spans := m.Highlight[line]
	if len(spans) == 0 {
		spans = []components.Span{{Text: m.Lines[line], Style: base}}
	} else {
		// Copy before touching Bg: callers cache Highlight across frames.
		spans = append([]components.Span(nil), spans...)
		if line == m.CursorLine {
			for i := range spans {
				spans[i].Style.Bg = base.Bg
			}
		}
	}
	if m.XScroll > 0 {
		spans = skipCols(spans, m.XScroll, method)
	}
	components.PaintSpans(s, gutterW, y, clipSpans(spans, codeW, method), method)
}

// paintGutter writes the right-aligned 1-based line number and the rule that
// separates it from the code. Number and rule carry different styles, so they
// are painted as two runs; total width is numW + gutterRuleWidth. Both inherit
// the row wash so a cursor line does not break at the gutter.
func paintGutter(
	s *components.Surface,
	y, numW, num int,
	th components.Theme,
	row xui.Style,
	method xui.WidthMethod,
) {
	text := strconv.Itoa(num)
	s.Print(numW-len(text), y, text, gutterStyle(th, row), method)
	s.Print(numW, y, gutterRule, ruleStyle(th, row), method)
}

func paintStatus(s *components.Surface, w, h int, m Model, method xui.WidthMethod) {
	th := m.Theme
	statusY := h - 1
	fillRow(s, statusY, w, th.Muted)
	s.Print(1, statusY, components.TruncateToWidth(m.Status, w/2, method), th.Muted, method)
	hint := m.Hint
	if hint == "" {
		hint = defaultHint
	}
	hw := xui.StringWidth(hint, method)
	if hw < w-2 {
		s.Print(w-1-hw, statusY, hint, th.Muted, method)
	}
}

// paintSelection tints the selected range. SelStart/SelEnd are document
// coordinates (Y = line index, X = display column); they are mapped to screen
// rows here, so lines scrolled out of view simply do not paint. Interior rows
// run edge to edge, matching diffview's active-row convention.
func paintSelection(s *components.Surface, m Model, scroll, gutterW, bodyH int) {
	x0, y0, x1, y1 := components.NormalizeSelectionOrder(m.SelStart.X, m.SelStart.Y, m.SelEnd.X, m.SelEnd.Y)
	for y := 1; y < 1+bodyH && y < s.Size.Height-1; y++ {
		line := scroll + y - 1
		if line < y0 || line > y1 {
			continue
		}
		fx0 := 0
		if line == y0 {
			fx0 = gutterW + x0 - m.XScroll
		}
		fx1 := s.Size.Width - 1
		if line == y1 {
			fx1 = gutterW + x1 - m.XScroll
		}
		if fx1 < 0 || fx0 > s.Size.Width-1 {
			continue
		}
		components.ApplySelectionHighlight(s, fx0, y, fx1, y, m.Theme.SelectionBg)
	}
}

// paintCursor exposes the caret cell so the host can point the terminal cursor
// at it. A caret that is scrolled away, past the body, or past the last line is
// dropped: pointing the terminal cursor at a cell the user cannot see is worse
// than showing no cursor at all.
func paintCursor(s *components.Surface, m Model, w, scroll, gutterW, bodyH int) {
	if m.CursorLine < 0 || m.CursorLine >= len(m.Lines) {
		return
	}
	y := 1 + m.CursorLine - scroll
	x := gutterW + m.CursorCol - m.XScroll
	if y < 1 || y >= 1+bodyH || x < gutterW || x >= w {
		return
	}
	s.Cursor = &components.Point{X: x, Y: y}
}

func clampScroll(scroll, lines int) int {
	if scroll < 0 {
		return 0
	}
	if scroll > lines-1 {
		return max(lines-1, 0)
	}
	return scroll
}

func lineNumberWidth(count int) int {
	if count < 1 {
		return 1
	}
	return len(strconv.Itoa(count))
}

func fillRow(s *components.Surface, y, w int, st xui.Style) {
	fillRange(s, 0, y, w, st)
}

func fillRange(s *components.Surface, x, y, w int, st xui.Style) {
	bg := xui.Style{Bg: st.Bg, Fg: st.Fg}
	for i := range w {
		s.SetCell(x+i, y, xui.Cell{Char: " ", Width: 1, Style: bg})
	}
}

// SkipCols drops the first cols display columns, so the remainder lines up with
// the pane's left edge after a horizontal scroll. Wide glyphs count their real
// width; the cluster straddling the cut is kept whole, because half a wide glyph
// has nowhere to go.
func skipCols(spans []components.Span, cols int, method xui.WidthMethod) []components.Span {
	if cols <= 0 {
		return spans
	}
	skipped := 0
	// Once the cut lands inside a cluster we keep that cluster whole and stop
	// skipping; without this the spans after it would each lose their head and
	// characters would vanish from the middle of the line.
	cut := false
	out := make([]components.Span, 0, len(spans))
	for _, sp := range spans {
		if cut {
			out = append(out, sp)
			continue
		}
		rest := sp.Text
		for rest != "" {
			cluster, cw, next := xui.FirstGrapheme(rest, method)
			rest = next
			if cw < 1 {
				cw = 1
			}
			if skipped+cw <= cols {
				skipped += cw
				continue
			}
			out = append(out, components.Span{Text: cluster + rest, Style: sp.Style})
			rest = ""
			cut = true
		}
	}
	return out
}

// ClipSpans keeps the longest prefix of spans that fits in width columns. A
// trailing wide glyph that does not fit is dropped rather than replaced by the
// next span's text, so a clipping pane never shows a character that is not
// where it claims to be.
func clipSpans(spans []components.Span, width int, method xui.WidthMethod) []components.Span {
	if width <= 0 {
		return nil
	}
	var out []components.Span
	used := 0
	for _, sp := range spans {
		if used >= width {
			break
		}
		if sp.Text == "" {
			continue
		}
		text := components.TruncateToWidth(sp.Text, width-used, method)
		if text == "" {
			break
		}
		out = append(out, components.Span{Text: text, Style: sp.Style})
		used += xui.StringWidth(text, method)
	}
	return out
}
