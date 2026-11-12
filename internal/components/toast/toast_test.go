package toast

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
)

func TestToastDrawSuccess(t *testing.T) {
	toast := Toast{Theme: components.DefaultTheme()}
	toast.Show("Selection copied to clipboard", ToastSuccess, time.Second)
	s := toast.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 24}, Method: xui.WidthUnicode})
	require.Len(t, s.Children, 1)
	panel := s.Children[0].Surface
	var row strings.Builder
	for x := 0; x < panel.Size.Width; x++ {
		ch := panel.Buffer[panel.Size.Width+x].Char // y=1 content row
		if ch == "" {
			ch = " "
		}
		row.WriteString(ch)
	}
	got := row.String()
	require.Contains(t, got, "Selection copied")
	require.Contains(t, got, "✓")
}

func TestToastDrawWrapsLongError(t *testing.T) {
	message := "error: the branch 'feature/long-branch-name' is not fully merged. " +
		"If you are sure you want to delete it, run 'git branch -D feature/long-branch-name'."
	toast := Toast{Theme: components.DefaultTheme()}
	toast.Show(message, ToastError, time.Second)
	s := toast.Draw(components.DrawContext{Max: components.Size{Width: 40, Height: 24}, Method: xui.WidthUnicode})
	require.Len(t, s.Children, 1)
	panel := s.Children[0].Surface
	require.Greater(t, panel.Size.Height, 3)
	require.LessOrEqual(t, s.Children[0].Origin.X+panel.Size.Width, 40)
	var rendered strings.Builder
	for y := 1; y < panel.Size.Height-1; y++ {
		require.Equal(t, "│", panel.Buffer[y*panel.Size.Width].Char)
		require.Equal(t, "│", panel.Buffer[(y+1)*panel.Size.Width-1].Char)
		for x := 3; x < panel.Size.Width-1; x++ {
			cell := panel.Buffer[y*panel.Size.Width+x]
			require.Equal(t, toast.Theme.Foreground.Fg, cell.Style.Fg)
			rendered.WriteString(cell.Char)
		}
	}
	require.Equal(t, message, strings.TrimRight(rendered.String(), " "))
	require.Equal(t, "✗", panel.Buffer[panel.Size.Width+1].Char)
	require.Equal(t, toast.Theme.Destructive, panel.Buffer[panel.Size.Width+1].Style)
}

func TestToastDrawMultiline(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
		width   int
		want    []string
	}{
		{"hard breaks", "first\n\nlast\n", 40, []string{"first", "", "last", ""}},
		{"wide glyphs", "你好世界", 8, []string{"你好世", "界"}},
		{"combining glyphs", strings.Repeat("e\u0301", 8), 8, []string{strings.Repeat("e\u0301", 6), strings.Repeat("e\u0301", 2)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			toast := Toast{}
			toast.Show(tc.message, ToastWarning, time.Second)
			s := toast.Draw(components.DrawContext{
				Max: components.Size{Width: tc.width, Height: 24}, Method: xui.WidthUnicode,
			})
			require.Len(t, s.Children, 1)
			panel := s.Children[0].Surface
			require.Equal(t, len(tc.want)+2, panel.Size.Height)
			for y, want := range tc.want {
				var row strings.Builder
				for x := 1; x < panel.Size.Width-1; x++ {
					cell := panel.Buffer[(y+1)*panel.Size.Width+x]
					if !cell.Trail {
						row.WriteString(cell.Char)
					}
				}
				require.Equal(t, want, strings.TrimRight(row.String(), " "))
				require.Equal(t, "│", panel.Buffer[(y+2)*panel.Size.Width-1].Char)
			}
		})
	}
}

func TestToastDrawFitsSmallTerminals(t *testing.T) {
	for width := 1; width <= 10; width++ {
		for height := 1; height <= 5; height++ {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				toast := Toast{}
				toast.Show(strings.Repeat("x", 100), ToastError, time.Second)
				s := toast.Draw(components.DrawContext{
					Max: components.Size{Width: width, Height: height}, Method: xui.WidthUnicode,
				})
				require.Len(t, s.Children, 1)
				child := s.Children[0]
				require.GreaterOrEqual(t, child.Origin.X, 0)
				require.GreaterOrEqual(t, child.Origin.Y, 0)
				require.LessOrEqual(t, child.Origin.X+child.Surface.Size.Width, width)
				require.LessOrEqual(t, child.Origin.Y+child.Surface.Size.Height, height)
				var rendered strings.Builder
				for _, cell := range child.Surface.Buffer {
					rendered.WriteString(cell.Char)
				}
				require.Contains(t, rendered.String(), "…")
				if width > 1 || height > 1 {
					require.Contains(t, rendered.String(), "x")
				}
			})
		}
	}
}
