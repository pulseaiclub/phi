package transcript

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pulseaiclub/xui"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
)

// rowStub is a fixed-height Widget used to exercise MessageList without
// importing the block subpackage (avoids components↔block test-type cycles).
type rowStub struct {
	text string
	h    int
}

func (*rowStub) Handle(_ *components.EventContext, _ xui.Event) {}

func (r *rowStub) Draw(ctx components.DrawContext) components.Surface {
	w := ctx.Max.Width
	w = max(w, 1)
	h := r.h
	h = max(h, 1)
	s := components.NewSurface(w, h, r)
	s.Print(0, 0, r.text, xui.Style{}, ctx.Method)
	return s
}

// lineStub renders one numbered line per row, so a selection can be checked
// row by row once the viewport boundary is crossed.
type lineStub struct{ h int }

func (*lineStub) Handle(_ *components.EventContext, _ xui.Event) {}

func (l *lineStub) Draw(ctx components.DrawContext) components.Surface {
	s := components.NewSurface(max(ctx.Max.Width, 1), max(l.h, 1), l)
	for y := range max(l.h, 1) {
		s.Print(0, y, stubLine(y), xui.Style{}, ctx.Method)
	}
	return s
}

// stubLine is what lineStub prints on row y, and so the text a copy of that row
// must hold.
func stubLine(y int) string { return fmt.Sprintf("a%02d", y) }

// requireStubRows asserts text is the stub's body line by line: one line per
// row 0..rows-1, in order, nothing missing and nothing extra.
func requireStubRows(t *testing.T, text string, rows int) {
	t.Helper()
	lines := strings.Split(text, "\n")
	require.Len(t, lines, rows, "got %d rows, want %d:\n%s", len(lines), rows, text)
	for i, line := range lines {
		require.Equal(t, stubLine(i), strings.TrimSpace(line), "row %d", i)
	}
}

// A drag selection is tracked in content space, so it can span rows the
// viewport virtualized away. Copying must render those rows, not read back the
// windowed surface (which would silently drop everything off screen).
func TestMessageListSelectionTextCoversOffscreenRows(t *testing.T) {
	const total, viewH = 30, 6
	list := &MessageList{Entries: []components.Widget{&lineStub{h: total}}}
	_ = list.Draw(components.DrawContext{Max: components.Size{Width: 40, Height: viewH}})
	require.Negative(t, list.ContentOrigin(), "setup: content must overflow the viewport")

	// Columns 1.. are the entry body (pad 1); rows 0..total-1 are the whole message.
	requireStubRows(t, list.SelectionText(1, 0, 39, total-1), total)
}

// A drag grabs interior lines whole, so the re-rendered surface must keep the
// list's full width: clipping it to the selection's x range would truncate
// every row between the two endpoints.
func TestMessageListSelectionTextKeepsWholeInteriorRows(t *testing.T) {
	list := &MessageList{Entries: []components.Widget{&lineStub{h: 4}}}
	_ = list.Draw(components.DrawContext{Max: components.Size{Width: 20, Height: 10}})

	// Columns 1..1, rows 0..2: the endpoints stop at the drag columns, and the
	// row between them is grabbed whole.
	lines := strings.Split(list.SelectionText(1, 0, 1, 2), "\n")
	require.Len(t, lines, 3)
	require.Equal(t, stubLine(0), lines[0])
	require.Equal(t, stubLine(1), strings.TrimSpace(lines[1]), "interior row must be copied whole")
	require.Equal(t, "a", strings.TrimSpace(lines[2]), "last row stops at the drag end column")
}

func TestMessageListSelectionTextClampsToContent(t *testing.T) {
	// Short content: the list bottom-anchors it under a blank margin, and a
	// selection dragged into that margin must not gain blank edge lines.
	list := &MessageList{Entries: []components.Widget{&lineStub{h: 3}}}
	_ = list.Draw(components.DrawContext{Max: components.Size{Width: 40, Height: 10}})
	require.Positive(t, list.ContentOrigin(), "setup: content must sit below the viewport top")

	requireStubRows(t, list.SelectionText(1, -4, 39, 8), 3)
}

func TestMessageListBottomPin(t *testing.T) {
	list := &MessageList{
		Entries: []components.Widget{
			&rowStub{text: "one", h: 1},
			&rowStub{text: "two", h: 1},
			&rowStub{text: "three", h: 1},
		},
	}
	s := list.Draw(components.DrawContext{Max: components.Size{Width: 40, Height: 4}})
	require.NotEmpty(t, s.Children, "expected visible children")
	last := s.Children[len(s.Children)-1]
	require.LessOrEqual(t, last.Origin.Y+last.Surface.Size.Height, 4,
		"last overflows: origin=%+v h=%d", last.Origin, last.Surface.Size.Height)
}

func TestMessageListInvalidateHeightsAt(t *testing.T) {
	list := &MessageList{
		Entries: []components.Widget{
			&rowStub{text: "a", h: 2},
			&rowStub{text: "b", h: 3},
			&rowStub{text: "c", h: 4},
		},
	}
	_ = list.Draw(components.DrawContext{Max: components.Size{Width: 40, Height: 20}})
	require.Equal(t, 2, list.CachedHeight(0))
	require.Equal(t, 3, list.CachedHeight(1))
	require.Equal(t, 4, list.CachedHeight(2))
	list.InvalidateHeightsAt(1)
	require.Equal(t, 2, list.CachedHeight(0))
	require.Equal(t, 0, list.CachedHeight(1))
	require.Equal(t, 4, list.CachedHeight(2))
	_ = list.Draw(components.DrawContext{Max: components.Size{Width: 40, Height: 20}})
	require.Equal(t, 3, list.CachedHeight(1))
}

func TestMessageListReindexHeights(t *testing.T) {
	list := &MessageList{
		Entries: []components.Widget{
			&rowStub{text: "a", h: 2},
			&rowStub{text: "b", h: 5},
			&rowStub{text: "c", h: 3},
		},
	}
	_ = list.Draw(components.DrawContext{Max: components.Size{Width: 40, Height: 20}})
	oldIDs := []string{"a", "b", "c"}
	// Insert "x" between a and b; heights must follow ids, not old indices.
	list.Entries = []components.Widget{
		&rowStub{text: "a", h: 2},
		&rowStub{text: "x", h: 7},
		&rowStub{text: "b", h: 5},
		&rowStub{text: "c", h: 3},
	}
	list.ReindexHeights(oldIDs, []string{"a", "x", "b", "c"})
	require.Equal(t, 2, list.CachedHeight(0))
	require.Equal(t, 0, list.CachedHeight(1))
	require.Equal(t, 5, list.CachedHeight(2))
	require.Equal(t, 3, list.CachedHeight(3))
	list.InvalidateHeightsAt(1)
	_ = list.Draw(components.DrawContext{Max: components.Size{Width: 40, Height: 20}})
	require.Equal(t, 7, list.CachedHeight(1))
}

func TestMessageListVirtualizes(t *testing.T) {
	const n = 80
	entries := make([]components.Widget, n)
	for i := range n {
		entries[i] = &rowStub{text: "row", h: 1}
	}
	list := &MessageList{Entries: entries}
	const viewH = 6
	s := list.Draw(components.DrawContext{Max: components.Size{Width: 40, Height: viewH}})
	require.Less(t, len(s.Children), n, "expected windowed draw, children=%d for %d entries", len(s.Children), n)
	require.LessOrEqual(
		t,
		len(s.Children),
		viewH+2,
		"too many realized children: %d (viewH=%d)",
		len(s.Children),
		viewH,
	)
	first, last := list.VisibleRange()
	require.GreaterOrEqual(t, first, 0, "visible range %d..%d", first, last)
	require.GreaterOrEqual(t, last, first, "visible range %d..%d", first, last)
	require.Equal(t, n-1, last, "bottom pin: last visible=%d want %d", last, n-1)
	list.ScrollFromBottom = 40
	s2 := list.Draw(components.DrawContext{Max: components.Size{Width: 40, Height: viewH}})
	f2, l2 := list.VisibleRange()
	require.NotEmpty(t, s2.Children, "scroll did not move window: %d..%d (was %d..%d)", f2, l2, first, last)
	require.False(t, l2 >= n-1 && f2 == first, "scroll did not move window: %d..%d (was %d..%d)", f2, l2, first, last)
}
