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
		require.Equal(t, len(o.perm.bodyLines(components.DefaultTheme(), width-4, 0))+2, height,
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
		// The hint is painted last; a blank last row means the tail was cut.
		rows := strings.Split(strings.TrimSuffix(plain, "\n"), "\n")
		require.Len(t, rows, height)
		last := strings.Trim(strings.TrimSuffix(strings.TrimPrefix(rows[height-2], "│"), "│"), " ")
		require.NotEmpty(t, last, "hint clipped at width %d", width)
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
