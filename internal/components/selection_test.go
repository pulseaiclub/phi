package components

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/xui"
)

func TestExtractSurfaceText(t *testing.T) {
	s := NewSurface(10, 3, nil)
	s.Print(0, 0, "hello", xui.Style{}, xui.WidthUnicode)
	s.Print(0, 1, "world", xui.Style{}, xui.WidthUnicode)
	got := ExtractSurfaceText(s, 0, 0, 4, 1)
	require.Equal(t, "hello\nworld", got)
	partial := ExtractSurfaceText(s, 1, 0, 3, 0)
	require.Equal(t, "ell", partial)
}

func TestExtractSurfaceTextCJKNoContinuationSpaces(t *testing.T) {
	// SetCell pads wide glyphs with Width=1 " " trail cells; copy must not
	// turn those into "二 进 制".
	s := NewSurface(20, 1, nil)
	s.Print(0, 0, "二进制文件 phi", xui.Style{}, xui.WidthUnicode)
	got := ExtractSurfaceText(s, 0, 0, 19, 0)
	want := "二进制文件 phi"
	require.Equal(t, want, got)
	// Selecting only the trail half of the first glyph still yields the rune.
	half := ExtractSurfaceText(s, 1, 0, 1, 0)
	require.Equal(t, "二", half)
}

func TestExtractSurfaceTextSkipsRuleChrome(t *testing.T) {
	s := NewSurface(20, 1, nil)
	s.SetCell(0, 0, xui.Cell{Char: "▎", Width: 1})
	s.SetCell(1, 0, xui.Cell{Char: " ", Width: 1})
	s.Print(2, 0, "你好", xui.Style{}, xui.WidthUnicode)
	got := ExtractSurfaceText(s, 0, 0, 19, 0)
	require.Equal(t, "你好", got)
}

// Titles carry a disclosure arrow; a selection across one is body text, and the
// clipboard should not inherit the transcript's chrome.
func TestExtractSurfaceTextSkipsDisclosureArrow(t *testing.T) {
	s := NewSurface(20, 1, nil)
	s.Print(0, 0, "$ ls", xui.Style{}, xui.WidthUnicode)
	s.Print(5, 0, " ▶", xui.Style{}, xui.WidthUnicode)
	require.Equal(t, "$ ls", ExtractSurfaceText(s, 0, 0, 19, 0))

	s.Print(5, 0, " ▼", xui.Style{}, xui.WidthUnicode)
	require.Equal(t, "$ ls", ExtractSurfaceText(s, 0, 0, 19, 0))
}

func TestInTextSelection(t *testing.T) {
	require.True(t, InTextSelection(2, 0, 0, 0, 5, 0), "mid single line")
	require.False(t, InTextSelection(0, 1, 2, 0, 5, 0), "below single line")
	require.True(t, InTextSelection(0, 1, 2, 0, 3, 2), "middle of multi-line")
}
