package overlays

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// Every decision must be reachable at any terminal width: PreferredBottomHeight
// and Draw read the same rows, so a wrapped label grows the panel instead of
// pushing the last option (or the hint) past the border.
func TestPermissionPanelFitsEveryOptionAtAnyWidth(t *testing.T) {
	for _, width := range []int{90, 80, 60, 48, 40, 34} {
		o := testOverlays(controller.NewActivityHandler())
		o.beginPermissionAsk(controller.OverlayMsg{
			Kind: controller.OverlayPermissionAsk,
			Request: permission.Request{
				Kind: permission.KindExec, Tool: "bash", Command: "go test ./...",
			},
			Reason: "runs the suite",
		})
		height, overlay := o.PreferredBottomHeight(width, 0)
		require.True(t, overlay)
		head, detail, choices := o.perm.segments(components.DefaultTheme(), askInnerWidth(width), 0)
		require.Equal(t, askNaturalHeight(head, detail, choices), height,
			"height must come from the same rows the panel paints (width %d)", width)

		surf := o.drawPermissionAsk(
			components.DrawContext{Max: components.Size{Width: width, Height: height}},
			width,
			height,
		)
		plain := components.SurfaceText(surf)
		// One marker per decision row: every choice rendered, however it wrapped.
		markers := strings.Count(plain, "○") + strings.Count(plain, "●")
		require.Equal(t, len(askOptionLabels), markers, "an option row was clipped at width %d", width)
		// The hint is painted last; a blank row above the border means the panel
		// reserved a row it did not use, or cut the tail.
		rows := strings.Split(strings.TrimSuffix(plain, "\n"), "\n")
		require.Len(t, rows, height)
		last := strings.Trim(strings.Trim(rows[height-2], "│"), " ")
		require.NotEmpty(t, last, "hint clipped or a blank row reserved at width %d", width)
	}
}

func TestAskPanelHeadersArePlainQuestions(t *testing.T) {
	tests := []struct {
		req  permission.Request
		want string
	}{
		{permission.Request{Kind: permission.KindExec, Tool: "bash", Command: "ls"}, "Run this command?"},
		{permission.Request{Kind: permission.KindWrite, Tool: "edit", Paths: []string{"a.go"}}, "Edit this file?"},
		{permission.Request{Kind: permission.KindWrite, Tool: "write", Paths: []string{"a.go"}}, "Write this file?"},
		{permission.Request{Kind: permission.KindRead, Tool: "grep"}, "Use the grep tool?"},
	}
	for _, tt := range tests {
		header, _ := formatAskHeader(tt.req)
		require.Equal(t, tt.want, header)
	}
}

func TestContinueAskNamesTheLimitInPlainWords(t *testing.T) {
	o := testOverlays(controller.NewActivityHandler())
	o.beginContinueAsk(controller.OverlayMsg{Kind: controller.OverlayContinueAsk, MaxRounds: 64})
	surf := o.drawContinueAsk(components.DrawContext{Max: components.Size{Width: 80, Height: 10}}, 80, 10)
	plain := components.SurfaceText(surf)
	require.Contains(t, plain, "64-step limit")
	require.NotContains(t, plain, "rounds", "internal jargon must not reach the user")
}

// A short terminal clamps the panel below its preferred height. The choices are
// the point of the panel, so the detail between question and choices is what
// gets trimmed — never an option row.
func TestAskPanelKeepsChoicesWhenClamped(t *testing.T) {
	o := testOverlays(controller.NewActivityHandler())
	o.beginPermissionAsk(controller.OverlayMsg{
		Kind: controller.OverlayPermissionAsk,
		Request: permission.Request{
			Kind:    permission.KindExec,
			Tool:    "bash",
			Command: "go test ./...\nline2\nline3",
		},
		Reason: "runs the suite",
	})
	preferred := o.perm.preferredAskHeight(60, 0)
	require.Greater(t, preferred, 8, "fixture must be tall enough to need trimming")

	for height := 8; height <= preferred; height++ {
		surf := o.drawPermissionAsk(components.DrawContext{Max: components.Size{Width: 60, Height: height}}, 60, height)
		plain := components.SurfaceText(surf)
		for _, label := range askOptionLabels {
			require.Contains(t, plain, label, "option %q lost at height %d", label, height)
		}
	}
}

// Body text must never paint over the frame: at narrow widths the wrap column
// has to shrink with the panel, not fall back to the full width.
func TestAskPanelNeverOverflowsItsBorder(t *testing.T) {
	for _, width := range []int{60, 40, 30, 24, 20, 16, 12} {
		o := testOverlays(controller.NewActivityHandler())
		o.beginPermissionAsk(controller.OverlayMsg{
			Kind: controller.OverlayPermissionAsk,
			Request: permission.Request{
				Kind:    permission.KindExec,
				Tool:    "bash",
				Command: "go test ./...",
			},
			Reason: "runs the suite",
		})
		height := o.perm.preferredAskHeight(width, 0)
		surf := o.drawPermissionAsk(
			components.DrawContext{Max: components.Size{Width: width, Height: height}},
			width,
			height,
		)
		for y := 1; y < height-1; y++ {
			right := surf.Buffer[y*width+width-1]
			require.Equal(t, "│", right.Char, "width %d row %d: text overwrote the right border", width, y)
		}
	}
}
