package layout

import (
	"strings"
	"testing"

	"github.com/pulseaiclub/xui"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
)

// rowString returns one painted row of s, mapping unpainted cells to spaces.
func rowString(s *components.Surface, y int) string {
	var b strings.Builder
	for x := 0; x < s.Size.Width; x++ {
		ch := s.Buffer[y*s.Size.Width+x].Char
		if ch == "" {
			ch = " "
		}
		b.WriteString(ch)
	}
	return b.String()
}

func TestBorderGlyphs(t *testing.T) {
	assert.Equal(t,
		borderChars{tl: "┌", tr: "┐", bl: "└", br: "┘", h: "─", v: "│"},
		borderGlyphs(BorderSquare), "square")

	assert.Equal(t,
		borderChars{tl: "╭", tr: "╮", bl: "╰", br: "╯", h: "─", v: "│"},
		borderGlyphs(BorderRounded), "rounded")
}

func TestDrawRoundedBorderPaintsCornersAndEdges(t *testing.T) {
	s := components.NewSurface(10, 4, nil)
	DrawRoundedBorder(&s, BorderRounded, xui.Style{}, nil, nil, nil, nil, xui.WidthUnicode)

	require.Equal(t, "╭────────╮", rowString(&s, 0), "top edge")
	assert.Equal(t, "│        │", rowString(&s, 1), "left and right edges")
	assert.Equal(t, "│        │", rowString(&s, 2), "left and right edges")
	assert.Equal(t, "╰────────╯", rowString(&s, 3), "bottom edge")

	square := components.NewSurface(6, 3, nil)
	DrawRoundedBorder(&square, BorderSquare, xui.Style{}, nil, nil, nil, nil, xui.WidthUnicode)

	require.Equal(t, "┌────┐", rowString(&square, 0), "square corners")
	assert.Equal(t, "│    │", rowString(&square, 1))
	assert.Equal(t, "└────┘", rowString(&square, 2))
}

func TestDrawRoundedBorderTooSmallIsNoOp(t *testing.T) {
	for _, size := range []components.Size{
		{Width: 1, Height: 4},
		{Width: 10, Height: 1},
		{Width: 1, Height: 1},
		{Width: 0, Height: 0},
	} {
		s := components.NewSurface(size.Width, size.Height, nil)
		DrawRoundedBorder(&s, BorderRounded, xui.Style{}, &BorderLabel{Text: "x"}, nil, nil, nil, xui.WidthUnicode)

		blank := strings.Repeat(" ", size.Width)
		for y := 0; y < size.Height; y++ {
			assert.Equal(t, blank, rowString(&s, y), "size %v row %d must stay untouched", size, y)
		}
	}
}

// drawTopRow draws a box of the given width with the labels embedded in its top
// edge and returns that row.
func drawTopRow(width int, left, right *BorderLabel) string {
	s := components.NewSurface(width, 3, nil)
	DrawRoundedBorder(&s, BorderRounded, xui.Style{}, left, right, nil, nil, xui.WidthUnicode)
	return rowString(&s, 0)
}

func TestDrawRoundedBorderEmbedsLabels(t *testing.T) {
	// Left labels start one border cell after the corner, right labels end one
	// border cell before it.
	require.Equal(t, "╭─Title────╮", drawTopRow(12, &BorderLabel{Text: "Title"}, nil), "left only")
	assert.Equal(t, "╭─────v1.2─╮", drawTopRow(12, nil, &BorderLabel{Text: "v1.2"}), "right only")
	assert.Equal(t,
		"╭─Title─v1.2─╮",
		drawTopRow(14, &BorderLabel{Text: "Title"}, &BorderLabel{Text: "v1.2"}),
		"both, no collision")

	// A left label wider than the box is truncated; the right label survives.
	assert.Equal(t,
		"╭─AAAAAR─╮",
		drawTopRow(10, &BorderLabel{Text: "AAAAAAAAAA"}, &BorderLabel{Text: "R"}),
		"collision truncates the left label")
}

func TestDrawRoundedBorderEmbedsBottomLabels(t *testing.T) {
	s := components.NewSurface(12, 3, nil)
	DrawRoundedBorder(&s, BorderRounded, xui.Style{}, nil, nil,
		&BorderLabel{Text: "b"}, &BorderLabel{Text: "9"}, xui.WidthUnicode)

	require.Equal(t, "╰─b──────9─╯", rowString(&s, 2), "bottom edge labels")
}

func TestBorderLabelVisible(t *testing.T) {
	assert.False(t, BorderLabel{}.Visible(), "empty label")
	assert.True(t, BorderLabel{Text: "x"}.Visible(), "text label")
	assert.True(t, BorderLabel{Spans: []BorderSpan{{Text: "x"}}}.Visible(), "spans label")
}

func TestBorderLabelWidth(t *testing.T) {
	assert.Equal(t, 0, borderLabelWidth(nil, xui.WidthUnicode), "nil label")
	assert.Equal(t, 0, borderLabelWidth(&BorderLabel{}, xui.WidthUnicode), "empty label")
	assert.Equal(t, 3, borderLabelWidth(&BorderLabel{Text: "abc"}, xui.WidthUnicode), "text")
	assert.Equal(
		t,
		7,
		borderLabelWidth(
			&BorderLabel{Text: "ignored", Spans: []BorderSpan{{Text: "ab"}, {Text: "cdefg"}}},
			xui.WidthUnicode,
		),
		"spans take precedence over text",
	)
	assert.Equal(t, 4, borderLabelWidth(&BorderLabel{Text: "中文"}, xui.WidthUnicode), "wide glyphs count two columns")
}

func TestFitBorderLabel(t *testing.T) {
	style := xui.Style{Bold: true}

	spans, width := fitBorderLabel(nil, 5, xui.WidthUnicode)
	require.Nil(t, spans, "nil label")
	require.Zero(t, width)

	spans, width = fitBorderLabel(&BorderLabel{Text: "abc"}, 0, xui.WidthUnicode)
	require.Nil(t, spans, "zero budget")
	require.Zero(t, width)

	spans, width = fitBorderLabel(&BorderLabel{}, 5, xui.WidthUnicode)
	require.Nil(t, spans, "empty text")
	require.Zero(t, width)

	// Text labels keep their style and truncate to the budget.
	spans, width = fitBorderLabel(&BorderLabel{Text: "abcdef", Style: style}, 3, xui.WidthUnicode)
	require.Equal(t, []fittedSpan{{Text: "abc", Style: style}}, spans)
	require.Equal(t, 3, width)

	// Spans are laid out in order until the column budget is exhausted.
	spans, width = fitBorderLabel(
		&BorderLabel{Spans: []BorderSpan{{Text: "ab", Style: style}, {Text: "cdef"}}},
		4, xui.WidthUnicode)
	require.Equal(t, []fittedSpan{{Text: "ab", Style: style}, {Text: "cd"}}, spans)
	require.Equal(t, 4, width)

	// Wide glyphs inside spans count two columns and never straddle the budget.
	spans, width = fitBorderLabel(&BorderLabel{Spans: []BorderSpan{{Text: "中文", Style: style}}}, 3, xui.WidthUnicode)
	require.Equal(t, []fittedSpan{{Text: "中", Style: style}}, spans)
	require.Equal(t, 2, width)

	// A span that cannot fit a single grapheme stops the loop so later spans
	// do not jump ahead of it.
	spans, width = fitBorderLabel(
		&BorderLabel{Spans: []BorderSpan{{Text: "中", Style: style}, {Text: "x"}}},
		1, xui.WidthUnicode)
	require.Empty(t, spans, "wide grapheme does not fit")
	require.Zero(t, width)
}

func TestDrawRoundedBorderEmbedsStyledSpans(t *testing.T) {
	left := &BorderLabel{Spans: []BorderSpan{{Text: "ab", Style: xui.Style{Bold: true}}, {Text: "cd"}}}
	require.Equal(t, "╭─abcd─────╮", drawTopRow(12, left, nil))
}

func TestTruncateToWidth(t *testing.T) {
	tests := []struct {
		name string
		s    string
		max  int
		want string
	}{
		{"fits untouched", "abc", 3, "abc"},
		{"wider max returns the whole string", "héllo", 10, "héllo"},
		{"ascii truncated", "abcdef", 3, "abc"},
		{"non-ascii truncated on a grapheme boundary", "héllo", 3, "hél"},
		{"combining mark stays with its base", "e\u0301x", 1, "e\u0301"},
		{"wide glyphs count two columns", "中文abc", 4, "中文"},
		{"wide glyph does not straddle the limit", "中文abc", 3, "中"},
		{"max smaller than the first wide glyph", "中", 1, ""},
		{"empty input", "", 3, ""},
		{"zero max", "abc", 0, ""},
		{"negative max", "abc", -1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, TruncateToWidth(tt.s, tt.max, xui.WidthUnicode))
		})
	}
}
