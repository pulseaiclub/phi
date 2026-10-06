package components

import (
	"strings"

	"github.com/pulseaiclub/xui"
)

// CopyTexter is implemented by transcript blocks that can be copied wholesale.
type CopyTexter interface {
	CopyText() string
}

// ClickToggler is implemented by collapsible transcript blocks. The transcript
// pane owns the click/drag decision — it is the only layer that knows whether a
// press became a drag-selection — so a block reports the hit and flips instead
// of toggling on every mouse press it happens to see.
type ClickToggler interface {
	// ClickAt toggles when a click lands on the disclosure title at
	// surface-local (x,y), reporting whether it toggled.
	ClickAt(x, y int) bool
}

// NormalizeSelectionOrder returns reading-order start/end for a drag selection.
func NormalizeSelectionOrder(ax, ay, ex, ey int) (x0, y0, x1, y1 int) {
	if ay > ey || (ay == ey && ax > ex) {
		return ex, ey, ax, ay
	}
	return ax, ay, ex, ey
}

// InTextSelection reports whether (x,y) lies in a line-oriented selection.
func InTextSelection(x, y, ax, ay, ex, ey int) bool {
	x0, y0, x1, y1 := NormalizeSelectionOrder(ax, ay, ex, ey)
	if y < y0 || y > y1 {
		return false
	}
	if y0 == y1 {
		return x >= x0 && x <= x1
	}
	if y == y0 {
		return x >= x0
	}
	if y == y1 {
		return x <= x1
	}
	return true
}

// ExtractSurfaceText collects characters inside the selection rectangle (surface-local).
// Wide glyphs (CJK) occupy multiple columns with space-padded continuation cells;
// only the primary cell is emitted so clipboard text has no gaps between CJK glyphs.
func ExtractSurfaceText(s Surface, ax, ay, ex, ey int) string {
	w, h := s.Size.Width, s.Size.Height
	if w < 1 || h < 1 {
		return ""
	}
	buf := make([]xui.Cell, w*h)
	for i := range buf {
		buf[i] = xui.EmptyCell()
	}
	flattenSurface(s, buf, w, h, 0, 0)

	x0, y0, x1, y1 := NormalizeSelectionOrder(ax, ay, ex, ey)
	var b strings.Builder
	for y := y0; y <= y1 && y < h; y++ {
		if y < 0 {
			continue
		}
		var line strings.Builder
		skippedChrome := false
		// Step by cell width — same as Surface.Render — so continuation
		// columns filled with " " are not copied as real spaces.
		for x := 0; x < w; {
			c := buf[y*w+x]
			step := int(c.Width)
			step = max(step, 1)
			selected := false
			for i := 0; i < step; i++ {
				if InTextSelection(x+i, y, x0, y0, x1, y1) {
					selected = true
					break
				}
			}
			if selected {
				ch := c.Char
				if ch == "" {
					ch = " "
				}
				// Skip UI chrome (user-block left rule, etc.) so clipboard
				// paste into the composer is plain text only.
				if isSelectionChrome(ch) {
					skippedChrome = true
					x += step
					continue
				}
				line.WriteString(ch)
			}
			x += step
		}
		text := strings.TrimRight(line.String(), " ")
		if skippedChrome {
			text = strings.TrimPrefix(text, " ")
		}
		if y > y0 {
			b.WriteByte('\n')
		}
		b.WriteString(text)
	}
	return strings.TrimRight(b.String(), "\n")
}

// ApplySelectionHighlight tints selected cells with style.Bg (and optional Fg).
func ApplySelectionHighlight(s *Surface, ax, ay, ex, ey int, style xui.Style) {
	applySelHighlight(s, 0, 0, ax, ay, ex, ey, style)
}

func applySelHighlight(s *Surface, ox, oy, ax, ay, ex, ey int, style xui.Style) {
	if s.Buffer != nil {
		for y := 0; y < s.Size.Height; y++ {
			for x := 0; x < s.Size.Width; x++ {
				gx, gy := ox+x, oy+y
				if !InTextSelection(gx, gy, ax, ay, ex, ey) {
					continue
				}
				c := s.Buffer[y*s.Size.Width+x]
				c.Style.Bg = style.Bg
				if style.Fg.Kind != 0 {
					c.Style.Fg = style.Fg
				}
				c.Default = false
				if c.Char == "" {
					c.Char = " "
					c.Width = 1
				}
				// Keep Trail pads as pads — do not promote them to real spaces.
				s.Buffer[y*s.Size.Width+x] = c
			}
		}
	}
	for i := range s.Children {
		ch := &s.Children[i]
		applySelHighlight(&ch.Surface, ox+ch.Origin.X, oy+ch.Origin.Y, ax, ay, ex, ey, style)
	}
}

func flattenSurface(s Surface, dst []xui.Cell, w, h, ox, oy int) {
	if s.Buffer != nil {
		for y := 0; y < s.Size.Height; y++ {
			for x := 0; x < s.Size.Width; x++ {
				dx, dy := ox+x, oy+y
				if dx < 0 || dy < 0 || dx >= w || dy >= h {
					continue
				}
				c := s.Buffer[y*s.Size.Width+x]
				if c.Default {
					continue
				}
				dst[dy*w+dx] = c
			}
		}
	}
	children := append([]SubSurface(nil), s.Children...)
	for i := range len(children) {
		for j := i + 1; j < len(children); j++ {
			if children[j].Z < children[i].Z {
				children[i], children[j] = children[j], children[i]
			}
		}
	}
	for _, ch := range children {
		flattenSurface(ch.Surface, dst, w, h, ox+ch.Origin.X, oy+ch.Origin.Y)
	}
}

// Buffer may be nil: aggregate surfaces built as literals (no NewSurface)
// carry no cells of their own and only paint children.
func ApplyBlockHighlight(s *Surface, style xui.Style) {
	if s.Buffer == nil {
		return
	}
	for i := range s.Buffer {
		c := s.Buffer[i]
		c.Style.Bg = style.Bg
		c.Default = false
		if c.Char == "" {
			c.Char = " "
			c.Width = 1
		}
		// Preserve Trail so continuation pads are not painted as real spaces.
		s.Buffer[i] = c
	}
	for i := range s.Children {
		ApplyBlockHighlight(&s.Children[i].Surface, style)
	}
}

// EntryCopyText returns CopyText() when w implements CopyTexter.
func EntryCopyText(w Widget) string {
	if c, ok := w.(CopyTexter); ok {
		return c.CopyText()
	}
	return ""
}

// isSelectionChrome reports glyphs used as transcript chrome, not message body.
// The disclosure arrows are chrome.Expand / chrome.Collapse spelled out: chrome
// imports this package, so the glyphs cannot be shared as constants.
func isSelectionChrome(ch string) bool {
	switch ch {
	case "▎", "▌": // user-block left rule
		return true
	case "▶", "▼": // expand / collapse disclosure
		return true
	default:
		return false
	}
}
