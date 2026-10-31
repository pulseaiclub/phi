package layout

import (
	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
)

// BorderStyle selects box-drawing characters.
type BorderStyle int

// Border styles for drawable boxes.
const (
	BorderRounded BorderStyle = iota
	BorderSquare
)

type borderChars struct {
	tl, tr, bl, br, h, v string
}

func borderGlyphs(s BorderStyle) borderChars {
	if s == BorderSquare {
		return borderChars{tl: "┌", tr: "┐", bl: "└", br: "┘", h: "─", v: "│"}
	}
	return borderChars{tl: "╭", tr: "╮", bl: "╰", br: "╯", h: "─", v: "│"}
}

// BorderSpan is a styled fragment inside a BorderLabel.
type BorderSpan struct {
	Text  string
	Style xui.Style
}

// BorderLabel is text embedded into a border edge.
// When Spans is non-empty it paints instead of Text/Style (multi-style embeds).
type BorderLabel struct {
	Text  string
	Style xui.Style
	Spans []BorderSpan
}

// Visible reports whether the label has anything to paint.
func (l BorderLabel) Visible() bool {
	if len(l.Spans) > 0 {
		return true
	}
	return l.Text != ""
}

func borderLabelWidth(l *BorderLabel, method xui.WidthMethod) int {
	if l == nil {
		return 0
	}
	if len(l.Spans) > 0 {
		w := 0
		for _, sp := range l.Spans {
			w += xui.StringWidth(sp.Text, method)
		}
		return w
	}
	return xui.StringWidth(l.Text, method)
}

type fittedSpan struct {
	Text  string
	Style xui.Style
}

// fitBorderLabel truncates a label into maxW columns once; paintBorderLabel reuses the result.
func fitBorderLabel(l *BorderLabel, maxW int, method xui.WidthMethod) (spans []fittedSpan, width int) {
	if l == nil || maxW <= 0 {
		return nil, 0
	}
	if len(l.Spans) == 0 {
		if l.Text == "" {
			return nil, 0
		}
		text := components.TruncateToWidth(l.Text, maxW, method)
		return []fittedSpan{{Text: text, Style: l.Style}}, xui.StringWidth(text, method)
	}
	remain := maxW
	out := make([]fittedSpan, 0, len(l.Spans))
	for _, sp := range l.Spans {
		if remain <= 0 {
			break
		}
		text := components.TruncateToWidth(sp.Text, remain, method)
		if text == "" {
			// Budget too small for this grapheme; stop so later spans don't jump ahead.
			break
		}
		tw := xui.StringWidth(text, method)
		out = append(out, fittedSpan{Text: text, Style: sp.Style})
		width += tw
		remain -= tw
	}
	return out, width
}

func paintFittedSpans(s *components.Surface, x, y int, spans []fittedSpan, method xui.WidthMethod) {
	for _, sp := range spans {
		s.Print(x, y, sp.Text, sp.Style, method)
		x += xui.StringWidth(sp.Text, method)
	}
}

// paintBorderLabel draws a label capped to maxW and returns the painted column width.
func paintBorderLabel(s *components.Surface, x, y, maxW int, l *BorderLabel, method xui.WidthMethod) int {
	spans, width := fitBorderLabel(l, maxW, method)
	paintFittedSpans(s, x, y, spans, method)
	return width
}

// DrawRoundedBorder paints a rounded (or square) box onto s and embeds labels
// into the top/bottom edges. Left and right labels keep a 1-cell border gap
// from the corners so text is not glued to ╭╮╰╯.
func DrawRoundedBorder(
	s *components.Surface,
	style BorderStyle,
	borderStyle xui.Style,
	topLeft, topRight, bottomLeft, bottomRight *BorderLabel,
	method xui.WidthMethod,
) {
	w, h := s.Size.Width, s.Size.Height
	if w < 2 || h < 2 {
		return
	}
	g := borderGlyphs(style)
	bs := borderStyle

	put := func(x, y int, ch string, st xui.Style) {
		s.SetCell(x, y, xui.Cell{Char: ch, Width: 1, Style: st})
	}

	// Corners + edges
	put(0, 0, g.tl, bs)
	put(w-1, 0, g.tr, bs)
	put(0, h-1, g.bl, bs)
	put(w-1, h-1, g.br, bs)
	for x := 1; x < w-1; x++ {
		put(x, 0, g.h, bs)
		put(x, h-1, g.h, bs)
	}
	for y := 1; y < h-1; y++ {
		put(0, y, g.v, bs)
		put(w-1, y, g.v, bs)
	}

	const cornerGap = 1 // border cells between corner and label text
	embed := func(y int, left, right *BorderLabel) {
		avail := w - 2 // between corners
		if avail < 1 {
			return
		}
		leftW := borderLabelWidth(left, method)
		rightW := borderLabelWidth(right, method)
		content := avail
		if left != nil && leftW > 0 {
			content -= cornerGap
		}
		if right != nil && rightW > 0 {
			content -= cornerGap
		}
		if content < 0 {
			content = 0
		}
		// Prefer right label if they collide.
		if leftW+rightW > content {
			if rightW >= content {
				rightW = content
				leftW = 0
			} else {
				leftW = content - rightW
			}
		}
		if left != nil && leftW > 0 {
			paintBorderLabel(s, 1+cornerGap, y, leftW, left, method)
		}
		if right != nil && rightW > 0 {
			spans, tw := fitBorderLabel(right, rightW, method)
			// Leave cornerGap cells of border before the right corner.
			x := w - 1 - cornerGap - tw
			x = max(x, 1)
			paintFittedSpans(s, x, y, spans, method)
		}
	}
	embed(0, topLeft, topRight)
	embed(h-1, bottomLeft, bottomRight)
}
