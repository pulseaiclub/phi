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
	panel := drawPanel(t, p)

	want := -1
	for row, item := range p.Items {
		line := panelRow(panel, 1+rowPad+row)
		col := strings.Index(line, item.Description)
		require.GreaterOrEqual(t, col, 0, "row %d missing description: %q", row, line)
		if want < 0 {
			want = col
			continue
		}
		assert.Equal(t, want, col, "descriptions must share one column: %q", line)
	}
}

// The selected row paints from the theme's shared selection roles — the same
// chrome listpicker and the config form use — not from hardcoded colors. The
// bar floats a cell inside the frame, and the hint on it stays legible.
func TestPickerDrawSelectionUsesTheme(t *testing.T) {
	th := components.DarculaTheme()
	p := &Picker{
		Theme:         th,
		Items:         []Item{{Path: "go.mod", Description: "module"}, {Path: "main.go"}},
		AnchorBottomY: 20,
		AnchorWidth:   60,
	}
	panel := drawPanel(t, p)
	w := panel.Size.Width
	y := 1 + rowPad

	for x := barX; x < w-barX; x++ {
		assert.Equal(t, th.SelectionBg.Bg, panel.Buffer[y*w+x].Style.Bg,
			"selected row stripe must use the theme selection background")
	}
	for _, x := range []int{1, w - 2} {
		assert.NotEqual(t, th.SelectionBg.Bg, panel.Buffer[y*w+x].Style.Bg,
			"the stripe must keep a gutter cell from the frame")
	}
	label := panel.Buffer[y*w+padL].Style // first label char inside the bar
	assert.Equal(t, th.SelectionFg.Fg, label.Fg)
	assert.True(t, label.Bold, "the highlighted label keeps its emphasis")

	line := panelRow(panel, y)
	descX := strings.Index(line, "module")
	require.GreaterOrEqual(t, descX, 0, "selected hint missing: %q", line)
	desc := panel.Buffer[y*w+descX].Style
	assert.Equal(t, th.SelectionFg.Fg, desc.Fg, "the selected hint must not sink into the bar as Muted")
	assert.False(t, desc.Bold, "only the label is bold on the selected row")
}

// A blank row above and below the list keeps rows off the frame.
func TestPickerDrawPadsFrame(t *testing.T) {
	p := &Picker{
		Theme:         components.DefaultTheme(),
		Items:         []Item{{Path: "a"}, {Path: "b"}},
		AnchorBottomY: 20,
		AnchorWidth:   40,
	}
	panel := drawPanel(t, p)
	h := panel.Size.Height

	assert.Equal(t, 2+2*rowPad+len(p.Items), h, "border + padding + rows")
	assert.Empty(t, strings.TrimSpace(panelInner(panel, 1)), "blank row above the list")
	assert.Empty(t, strings.TrimSpace(panelInner(panel, h-2)), "blank row below the list")
	assert.Contains(t, panelRow(panel, 1+rowPad), "@a")
}

// Height pressure drops the padding before any row, and only then the rows.
func TestPickerDrawDropsPaddingUnderPressure(t *testing.T) {
	items := make([]Item, 5)
	for i := range items {
		items[i] = Item{Path: fmt.Sprintf("f%d", i)}
	}
	panelAt := func(anchor int) components.Surface {
		return drawPanel(t, &Picker{
			Theme:         components.DefaultTheme(),
			Items:         items,
			AnchorBottomY: anchor,
			AnchorWidth:   40,
		})
	}

	fits := panelAt(2 + 2*rowPad + len(items) + 1)
	assert.Equal(t, 2+2*rowPad+len(items), fits.Size.Height, "padding fits")

	tight := panelAt(2 + len(items) + 1)
	assert.Equal(t, 2+len(items), tight.Size.Height, "padding goes first, rows stay")
	assert.Contains(t, panelRow(tight, 1), "@f0")

	cramped := panelAt(2 + 2 + 1)
	assert.Equal(t, 2+2, cramped.Size.Height, "then rows go")
}

// The highlighted row stays in view when the list scrolls.
func TestPickerDrawScrollsSelectionIntoView(t *testing.T) {
	items := make([]Item, 20)
	for i := range items {
		items[i] = Item{Path: fmt.Sprintf("cmd%d", i)}
	}
	p := &Picker{
		Theme:         components.DefaultTheme(),
		Items:         items,
		MaxItems:      5,
		Prefix:        "/",
		AnchorBottomY: 20,
		AnchorWidth:   60,
		Selected:      len(items) - 1,
	}
	panel := drawPanel(t, p)
	last := panelRow(panel, panel.Size.Height-2-rowPad)
	assert.Contains(t, last, "/cmd19", "selection scrolled into view")
	assert.NotContains(t, last, "/cmd14")
}

// With no items the status message is the one row, padded like the list.
func TestPickerDrawEmptyState(t *testing.T) {
	p := &Picker{
		Theme:         components.DefaultTheme(),
		Status:        "No matching files",
		AnchorBottomY: 20,
		AnchorWidth:   60,
	}
	panel := drawPanel(t, p)
	assert.Equal(t, 2+2*rowPad+1, panel.Size.Height)
	assert.Contains(t, panelRow(panel, 1+rowPad), "No matching files")
}

// drawPanel opens p, draws it on an 80x24 screen, and returns the panel.
func drawPanel(t *testing.T, p *Picker) components.Surface {
	t.Helper()
	p.Show()
	surf := p.Draw(components.DrawContext{
		Max:    components.Size{Width: 80, Height: 24},
		Method: xui.WidthUnicode,
	})
	require.Len(t, surf.Children, 1)
	return surf.Children[0].Surface
}

// panelRow flattens one surface row into plain text for alignment assertions.
func panelRow(s components.Surface, y int) string {
	var b strings.Builder
	for x := range s.Size.Width {
		b.WriteString(s.Buffer[y*s.Size.Width+x].Char)
	}
	return b.String()
}

// panelInner is panelRow without the border columns.
func panelInner(s components.Surface, y int) string {
	row := []rune(panelRow(s, y))
	if len(row) < 2 {
		return ""
	}
	return string(row[1 : len(row)-1])
}
