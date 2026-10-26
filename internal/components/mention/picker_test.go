package mention

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pulseaiclub/xui"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
)

func TestPickerAccept(t *testing.T) {
	var got string
	p := &Picker{
		Items: []Item{{Path: "go.mod"}, {Path: "a/b.go"}},
		OnAccept: func(item Item) {
			got = item.Path
		},
	}
	p.Show()
	p.Selected = 1
	require.True(t, p.Accept(), "accept failed")
	require.Equal(t, "a/b.go", got)
	require.False(t, p.Open, "should be closed")
}

func TestPickerHandleNav(t *testing.T) {
	p := &Picker{
		Items: []Item{{Path: "a"}, {Path: "b"}, {Path: "c"}},
	}
	p.Show()
	require.True(t, p.HandleNav(xui.KeyEvent{Press: true, Code: xui.KeyDown}), "expected consume")
	require.Equal(t, 1, p.Selected)
	require.True(t, p.HandleNav(xui.KeyEvent{Press: true, Code: xui.KeyEscape}), "expected consume")
	require.False(t, p.Open, "should close on escape")
}

func TestPickerTabCompletesWithoutAccepting(t *testing.T) {
	var accepted, completed string
	p := &Picker{
		Items:      []Item{{Path: "new"}, {Path: "compact"}},
		OnAccept:   func(item Item) { accepted = item.Path },
		OnComplete: func(item Item) { completed = item.Path },
	}
	p.Show()
	p.Selected = 1

	require.True(t, p.HandleNav(xui.KeyEvent{Press: true, Code: xui.KeyTab}), "expected consume")
	assert.Equal(t, "compact", completed, "Tab should complete the highlighted item")
	assert.Empty(t, accepted, "Tab must not accept")
	assert.Equal(t, 1, p.Selected, "Tab must not move the selection")
	assert.False(t, p.Open, "should be closed")
}

func TestPickerShiftTabCompletes(t *testing.T) {
	var completed string
	p := &Picker{
		Items:      []Item{{Path: "a"}, {Path: "b"}},
		OnComplete: func(item Item) { completed = item.Path },
	}
	p.Show()
	p.Selected = 1

	require.True(t, p.HandleNav(xui.KeyEvent{Press: true, Code: xui.KeyTab, Mods: xui.ModShift}))
	assert.Equal(t, "b", completed, "Shift+Tab should no longer step back a row")
}

func TestPickerTabFallsBackToAccept(t *testing.T) {
	var got string
	p := &Picker{
		Items:    []Item{{Path: "go.mod"}},
		OnAccept: func(item Item) { got = item.Path },
	}
	p.Show()

	require.True(t, p.HandleNav(xui.KeyEvent{Press: true, Code: xui.KeyTab}))
	assert.Equal(t, "go.mod", got, "without OnComplete, Tab completes via OnAccept")
}

func TestPickerTabClosesOnEmptyResults(t *testing.T) {
	accepted := false
	p := &Picker{
		Status:   "No matching commands",
		OnAccept: func(Item) { accepted = true },
	}
	p.Show()

	require.True(t, p.HandleNav(xui.KeyEvent{Press: true, Code: xui.KeyTab}))
	assert.False(t, accepted, "nothing to complete")
	assert.False(t, p.Open, "Tab on an empty picker should close it, not stick")
}

func TestPickerDrawClosed(t *testing.T) {
	p := &Picker{Theme: components.DefaultTheme()}
	surf := p.Draw(components.DrawContext{
		Max:    components.Size{Width: 80, Height: 24},
		Method: xui.WidthUnicode,
	})
	require.Empty(t, surf.Children, "closed picker should have no children")
}

func TestPickerDrawOpen(t *testing.T) {
	p := &Picker{
		Theme:         components.DefaultTheme(),
		Items:         []Item{{Path: "go.mod"}, {Path: "internal/x.go"}},
		AnchorBottomY: 20,
		AnchorWidth:   60,
		AnchorX:       0,
	}
	p.Show()
	surf := p.Draw(components.DrawContext{
		Max:    components.Size{Width: 80, Height: 24},
		Method: xui.WidthUnicode,
	})
	require.Len(t, surf.Children, 1)
	child := surf.Children[0]
	require.LessOrEqual(t, child.Origin.Y+child.Surface.Size.Height, 20,
		"panel should sit above anchor: oy=%d h=%d", child.Origin.Y, child.Surface.Size.Height)
}

func TestPickerAlignsDescriptions(t *testing.T) {
	p := &Picker{
		Theme: components.DefaultTheme(),
		Items: []Item{
			{Path: "sessions", Description: "Browse sessions"},
			{Path: "branch", Description: "Switch branch"},
			{Path: "diff", Description: "Review diff"},
		},
		Prefix:        "/",
		AnchorBottomY: 20,
		AnchorWidth:   60,
	}
	p.Show()
	surf := p.Draw(components.DrawContext{
		Max:    components.Size{Width: 80, Height: 24},
		Method: xui.WidthUnicode,
	})
	require.Len(t, surf.Children, 1)
	panel := surf.Children[0].Surface

	want := -1
	for row, item := range p.Items {
		line := panelRow(panel, 1+row)
		col := strings.Index(line, item.Description)
		require.GreaterOrEqual(t, col, 0, "row %d missing description: %q", row, line)
		if want < 0 {
			want = col
			continue
		}
		assert.Equal(t, want, col, "descriptions must share one column: %q", line)
	}
}

// panelRow flattens one surface row into plain text for alignment assertions.
func panelRow(s components.Surface, y int) string {
	var b strings.Builder
	for x := range s.Size.Width {
		b.WriteString(s.Buffer[y*s.Size.Width+x].Char)
	}
	return b.String()
}

// A list longer than the window says so on the edge: ▼ while there are rows
// below, ▲ once the window has scrolled, neither when the list fits.
func TestPickerMarksAListLongerThanTheWindow(t *testing.T) {
	items := make([]Item, 30)
	for i := range items {
		items[i] = Item{Path: fmt.Sprintf("item-%02d", i)}
	}
	p := &Picker{
		Theme:         components.DefaultTheme(),
		Items:         items,
		MaxItems:      5,
		AnchorBottomY: 20,
		AnchorWidth:   60,
		AnchorX:       0,
	}
	p.Show()

	edge := func(surf components.Surface) (top, bottom string) {
		panel := surf.Children[0].Surface
		w := panel.Size.Width
		top = panel.Buffer[1*w+w-2].Char
		bottom = panel.Buffer[5*w+w-2].Char
		return top, bottom
	}

	surf := p.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 24}, Method: xui.WidthUnicode})
	top, bottom := edge(surf)
	assert.Equal(t, "▼", bottom, "rows below the window are marked")
	assert.Equal(t, " ", top, "the top of the list has nothing above it")

	p.Selected = 15
	surf = p.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 24}, Method: xui.WidthUnicode})
	top, bottom = edge(surf)
	assert.Equal(t, "▲", top, "a scrolled window marks rows above")
	assert.Equal(t, "▼", bottom, "and still rows below")

	p.Selected = len(items) - 1
	surf = p.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 24}, Method: xui.WidthUnicode})
	top, bottom = edge(surf)
	assert.Equal(t, "▲", top, "the bottom of the list still marks rows above")
	assert.Equal(t, " ", bottom, "but nothing below")
}

// A list that fits the window carries no scroll marks.
func TestPickerMarksNothingWhenTheListFits(t *testing.T) {
	p := &Picker{
		Theme:         components.DefaultTheme(),
		Items:         []Item{{Path: "a"}, {Path: "b"}},
		AnchorBottomY: 20,
		AnchorWidth:   60,
		AnchorX:       0,
	}
	p.Show()
	surf := p.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 24}, Method: xui.WidthUnicode})
	panel := surf.Children[0].Surface
	w := panel.Size.Width
	assert.Equal(t, " ", panel.Buffer[1*w+w-2].Char)
	assert.Equal(t, " ", panel.Buffer[2*w+w-2].Char)
}
