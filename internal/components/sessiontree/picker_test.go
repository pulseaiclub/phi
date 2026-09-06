package sessiontree

import (
	"fmt"
	"testing"
	"unicode/utf8"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
)

func treeKey(p *Picker, code xui.KeyCode, mods xui.Modifiers, r rune) {
	p.Handle(&components.EventContext{}, xui.KeyEvent{Code: code, Mods: mods, Rune: r, Press: true})
}

func TestTreeFilteringPromotesVisibleParentsAndPrioritizesActiveBranch(t *testing.T) {
	items := []Item{
		{ID: "u1", Kind: "user", Text: "first"},
		{ID: "empty", ParentID: "u1", Kind: "assistant"},
		{ID: "tool", ParentID: "empty", Kind: "tool", Text: "result"},
		{ID: "old", ParentID: "tool", Kind: "user", Text: "old branch"},
		{ID: "active", ParentID: "tool", Kind: "user", Text: "active branch", Label: "checkpoint"},
	}
	p := &Picker{}
	p.Show(items, "active", "all", nil)
	rows := p.Rows()
	require.Len(t, rows, 5)
	assert.Equal(t, "tool", rows[2].Item.ID)
	assert.Equal(t, "u1", rows[2].ParentID)
	assert.Equal(t, "active", rows[3].Item.ID)
	assert.True(t, rows[3].Active)
	assert.Equal(t, "old", rows[4].Item.ID)
	p.Filter = "user-only"
	p.rebuild("active")
	rows = p.Rows()
	require.Len(t, rows, 4)
	assert.Equal(t, "u1", rows[2].ParentID)
	assert.Equal(t, 1, rows[2].Depth)
	assert.Equal(t, "active", p.selected().ID)
	p.Filter = "labeled-only"
	p.rebuild("active")
	require.Len(t, p.Rows(), 2)
	assert.Equal(t, 0, p.Rows()[1].Depth)
	assert.Empty(t, p.Rows()[1].ParentID)
}

func TestTreeSearchUsesAllWordsAndPreservesVisibleAncestor(t *testing.T) {
	p := &Picker{}
	p.Show(
		[]Item{
			{ID: "a", Kind: "user", Text: "needle 中文"},
			{ID: "b", ParentID: "a", Kind: "assistant", Text: "needle only"},
			{ID: "c", ParentID: "a", Kind: "user", Text: "中文 only"},
		},
		"b",
		"all",
		nil,
	)
	p.Handle(&components.EventContext{}, xui.PasteEvent{Text: "needle 中文"})
	require.Len(t, p.Rows(), 2)
	assert.Equal(t, "a", p.selected().ID)
	treeKey(p, xui.KeyBackspace, 0, 0)
	assert.True(t, utf8.ValidString(p.Query))
	assert.Equal(t, "needle 中", p.Query)
}

func TestTreeFoldPageFilterLabelCopyAndCancel(t *testing.T) {
	p := &Picker{}
	items := []Item{
		{ID: "a", Kind: "user", Text: "root"},
		{ID: "b", ParentID: "a", Kind: "assistant", Text: "answer"},
		{ID: "c", ParentID: "b", Kind: "user", Text: "next"},
	}
	p.Show(items, "c", "all", nil)
	p.selectID("a")
	treeKey(p, xui.KeyLeft, 0, 0)
	require.Len(t, p.Rows(), 2)
	treeKey(p, xui.KeyRight, 0, 0)
	require.Len(t, p.Rows(), 4)
	p.page = 2
	treeKey(p, xui.KeyPageDown, 0, 0)
	assert.Equal(t, "c", p.selected().ID)
	var copied, label string
	p.OnCopy = func(s string) { copied = s }
	p.OnLabel = func(item Item, text string) error { assert.Equal(t, "c", item.ID); label = text; return nil }
	treeKey(p, xui.KeyRune, xui.ModCtrl, 'y')
	assert.Equal(t, "next", copied)
	treeKey(p, xui.KeyRune, xui.ModCtrl, 'l')
	p.Handle(&components.EventContext{}, xui.PasteEvent{Text: "saved"})
	treeKey(p, xui.KeyEnter, 0, 0)
	assert.Equal(t, "saved", label)
	assert.Equal(t, "browse", p.Mode)
	treeKey(p, xui.KeyTab, 0, 0)
	assert.Equal(t, "default", p.Filter)
	var cancelled bool
	p.Mode = "wait"
	p.OnCancel = func() { cancelled = true }
	treeKey(p, xui.KeyEscape, 0, 0)
	assert.True(t, cancelled)
	assert.True(t, p.Open)
	assert.Equal(t, "wait", p.Mode)
	p.Mode = "summary"
	treeKey(p, xui.KeyEscape, 0, 0)
	assert.Equal(t, "browse", p.Mode)
	assert.Equal(t, "c", p.selected().ID)
	treeKey(p, xui.KeyEscape, 0, 0)
	assert.False(t, p.Open)
}

func TestTreeCustomSummaryAndConfiguredKeys(t *testing.T) {
	p := &Picker{}
	p.Show([]Item{{ID: "a", Kind: "user", Text: "question"}}, "a", "all", map[string]string{"copy": "ctrl+o"})
	var copied string
	p.OnCopy = func(s string) { copied = s }
	treeKey(p, xui.KeyRune, xui.ModCtrl, 'o')
	assert.Equal(t, "question", copied)
	p.Mode, p.Choice = "summary", 2
	treeKey(p, xui.KeyEnter, 0, 0)
	assert.Equal(t, "prompt", p.Mode)
	p.Handle(&components.EventContext{}, xui.PasteEvent{Text: "keep decisions"})
	var instructions string
	p.OnChoice = func(summarize bool, text string) { assert.True(t, summarize); instructions = text }
	treeKey(p, xui.KeyEnter, 0, 0)
	assert.Equal(t, "keep decisions", instructions)
}

func TestTreeBranchJumpSkipsLinearSegments(t *testing.T) {
	p := &Picker{}
	p.Show([]Item{
		{ID: "root", Kind: "user", Text: "root"},
		{ID: "a", ParentID: "root", Kind: "user", Text: "branch a"},
		{ID: "a1", ParentID: "a", Kind: "assistant", Text: "linear"},
		{ID: "a2", ParentID: "a1", Kind: "user", Text: "linear"},
		{ID: "b", ParentID: "root", Kind: "user", Text: "branch b"},
	}, "a2", "all", nil)
	p.selectID("a")
	treeKey(p, xui.KeyDown, xui.ModCtrl, 0)
	assert.Equal(t, "b", p.selected().ID)
	treeKey(p, xui.KeyUp, xui.ModCtrl, 0)
	assert.Equal(t, "a", p.selected().ID)
}

func TestTreeDeepUnicodeAndTinySurfaces(t *testing.T) {
	var items []Item
	parent := ""
	for i := range 200 {
		id := fmt.Sprint(i)
		items = append(items, Item{ID: id, ParentID: parent, Kind: "user", Text: "中文 🙂 deep node"})
		parent = id
	}
	p := &Picker{Theme: components.DefaultTheme()}
	p.Show(items, parent, "all", nil)
	for _, width := range []int{1, 8, 20, 80} {
		for _, height := range []int{1, 3, 10} {
			for _, mode := range []string{"browse", "summary", "prompt", "label", "wait"} {
				p.Mode = mode
				surf := p.Draw(components.DrawContext{Max: components.Size{Width: width, Height: height}})
				assert.Len(t, surf.Buffer, width*height)
				assert.Equal(t, components.Size{Width: width, Height: height}, surf.Size)
				for _, cell := range surf.Buffer {
					assert.True(t, utf8.ValidString(cell.Char))
				}
			}
		}
	}
}
