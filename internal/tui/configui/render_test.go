package configui

import (
	"bytes"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
)

func TestDisplayPathKeepsFullPath(t *testing.T) {
	home := filepath.FromSlash("/users/phi")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	assert.Equal(t, "~/a/b/c/d/config.yaml", displayPath(filepath.Join(home, "a", "b", "c", "d", "config.yaml")))
}

func assertFocusMarker(t *testing.T, e *ConfigEditor, out components.Surface) {
	t.Helper()
	y := formRowY(t, e, e.rows[e.cursor].key)
	require.GreaterOrEqual(t, y, e.rowsTop)
	require.Less(t, y, e.rowsTop+e.page)
	for rowY := e.rowsTop; rowY < e.rowsTop+e.page; rowY++ {
		marker := out.Buffer[rowY*out.Size.Width+xSection]
		if rowY == y {
			assert.Equal(t, chrome.FocusMarker, marker.Char, "the active row must be identifiable without color")
			assert.True(t, marker.Style.Bold)
			assert.False(t, marker.Style.Dim)
		} else {
			assert.NotEqual(t, chrome.FocusMarker, marker.Char, "only the active row gets a focus marker")
		}
	}
}

func TestFocusMarkerFollowsNavigationAndScroll(t *testing.T) {
	e := newTestEditor(t, sampleDoc())
	ctx := components.DrawContext{Max: components.Size{Width: 40, Height: 8}, Method: xui.WidthUnicode}
	for range len(e.rows) {
		out := e.Draw(ctx)
		assertFocusMarker(t, e, out)
		r := e.rows[e.cursor]
		if r.kind == rowField {
			assert.True(t, paintedStyle(t, out, labelX(r.key), formRowY(t, e, r.key)).Bold,
				"the focused field label must stand out: %s", r.key)
		}
		press(e, xui.KeyDown, 0)
	}
	require.Positive(t, e.scroll, "navigation must exercise scrolling")
	press(e, xui.KeyHome, 0)
	assertFocusMarker(t, e, e.Draw(ctx))
	assert.Equal(t, rowModel, e.rows[e.cursor].kind)
}

func TestFocusMarkerRemainsDuringInlineAndRuleEditing(t *testing.T) {
	e := newTestEditor(t, sampleDoc())
	focus(t, e, modelKey(0, fName))
	press(e, xui.KeyEnter, 0)
	out := drawForm(t, e)
	assertFocusMarker(t, e, out)
	require.NotNil(t, out.Cursor)
	assert.Equal(t, formRowY(t, e, modelKey(0, fName)), out.Cursor.Y)
	press(e, xui.KeyEscape, 0)

	focus(t, e, keyPermBashAllow)
	press(e, xui.KeyEnter, 0)
	require.Equal(t, rowItem, e.rows[e.cursor].kind)
	assertFocusMarker(t, e, drawForm(t, e))
	press(e, xui.KeyEnter, 0)
	out = drawForm(t, e)
	assertFocusMarker(t, e, out)
	require.NotNil(t, out.Cursor)
	assert.Equal(t, formRowY(t, e, e.rows[e.cursor].key), out.Cursor.Y)
	press(e, xui.KeyEscape, 0)

	focus(t, e, listAddKey(keyPermBashAllow))
	require.Equal(t, rowAdd, e.rows[e.cursor].kind)
	assertFocusMarker(t, e, drawForm(t, e))
	press(e, xui.KeyEnter, 0)
	assertFocusMarker(t, e, drawForm(t, e))
}

func TestFocusMarkerSurvivesTerminalColorDowngrade(t *testing.T) {
	for _, name := range components.ThemeNames() {
		for _, level := range []xui.ColorLevel{xui.ColorNone, xui.ColorBasic, xui.Color256, xui.ColorTrue} {
			t.Run(name+"/"+strconv.Itoa(int(level)), func(t *testing.T) {
				e := newTestEditor(t, sampleDoc())
				var ok bool
				e.theme, ok = components.ThemeByName(name)
				require.True(t, ok)
				focus(t, e, modelKey(0, fURL))
				out := drawForm(t, e)
				y := formRowY(t, e, modelKey(0, fURL))
				marker := out.Buffer[y*out.Size.Width+xSection]
				if level != xui.ColorNone {
					assert.NotEqual(t, marker.Style.Fg.Downgrade(level), marker.Style.Bg.Downgrade(level),
						"the marker needs readable contrast after color conversion")
				}
				dirty := []xui.DirtyCell{{X: xSection, Y: y, Cell: marker}}
				renderer := xui.NewRenderer()
				renderer.UpdateCaps(xui.Caps{ColorLevel: level})
				var ansi bytes.Buffer
				_, err := renderer.RenderDiff(&ansi, dirty, 0, 0, false, 0)
				require.NoError(t, err)
				assert.Contains(
					t,
					ansi.String(),
					chrome.FocusMarker,
					"the actual terminal output must retain the focus marker",
				)
				assert.Contains(t, ansi.String(), "\x1b[1m", "focus emphasis must survive monochrome output")
			})
		}
	}
}
