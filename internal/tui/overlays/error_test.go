package overlays

import (
	"strings"
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func permissionRequest() permission.Request {
	return permission.Request{Kind: permission.KindExec, Tool: "bash", Command: "true"}
}

const testErrText = "request ended without sending any chunks"

// errorOverlays builds overlays whose resolve callback records replies.
func errorOverlays(replies *[]controller.ErrorReply, texts *[]string) *Overlays {
	return NewOverlays(
		components.DefaultTheme(),
		controller.NewActivityHandler(),
		nil, nil, nil,
		func(r controller.ErrorReply, text string) {
			*replies = append(*replies, r)
			*texts = append(*texts, text)
		},
	)
}

func beginErrorAsk(o *Overlays) {
	o.beginErrorAsk(controller.OverlayMsg{Kind: controller.OverlayErrorAsk, ErrorText: testErrText})
}

func TestErrorAskRendersAndResolves(t *testing.T) {
	var replies []controller.ErrorReply
	var texts []string
	o := errorOverlays(&replies, &texts)
	beginErrorAsk(o)
	require.NotNil(t, o.err, "expected errorAsk")
	require.True(t, o.Active(), "error dialog must block the composer")

	surf := o.drawError(components.DrawContext{Max: components.Size{Width: 60, Height: 12}}, 60, 12)
	require.Equal(t, 60, surf.Size.Width)
	require.Equal(t, 12, surf.Size.Height)

	h, overlay := o.PreferredBottomHeight(60, 0)
	require.True(t, overlay)
	require.GreaterOrEqual(t, h, 8)

	o.acceptErrorOption(errOptRetry)
	require.Nil(t, o.err, "expected cleared")
	require.Len(t, replies, 1)
	require.True(t, replies[0].Retry)
	require.False(t, replies[0].Copy)
	require.Equal(t, testErrText, texts[0], "resolve callback gets the error text")
}

func TestErrorAskKeyNavigation(t *testing.T) {
	var replies []controller.ErrorReply
	var texts []string
	o := errorOverlays(&replies, &texts)
	beginErrorAsk(o)
	ctx := &components.EventContext{}

	// Down ×2 → Dismiss; Up wraps back to Copy.
	o.handleErrorKey(ctx, xui.KeyEvent{Press: true, Code: xui.KeyDown})
	o.handleErrorKey(ctx, xui.KeyEvent{Press: true, Code: xui.KeyDown})
	require.Equal(t, errOptDismiss, o.err.selected)
	o.handleErrorKey(ctx, xui.KeyEvent{Press: true, Code: xui.KeyUp})
	require.Equal(t, errOptCopy, o.err.selected)

	// Enter on Copy resolves with Copy=true.
	o.handleErrorKey(ctx, xui.KeyEvent{Press: true, Code: xui.KeyEnter})
	require.Len(t, replies, 1)
	require.True(t, replies[0].Copy)
	require.False(t, replies[0].Retry)
	require.Nil(t, o.err)
}

func TestErrorAskEscapeDismisses(t *testing.T) {
	var replies []controller.ErrorReply
	var texts []string
	o := errorOverlays(&replies, &texts)
	beginErrorAsk(o)
	ctx := &components.EventContext{}
	o.handleErrorKey(ctx, xui.KeyEvent{Press: true, Code: xui.KeyEscape})
	require.Len(t, replies, 1, "escape should resolve")
	require.False(t, replies[0].Retry)
	require.False(t, replies[0].Copy)
}

func TestErrorDismissClearsOverlay(t *testing.T) {
	var replies []controller.ErrorReply
	var texts []string
	o := errorOverlays(&replies, &texts)
	beginErrorAsk(o)
	o.Apply(controller.OverlayMsg{Kind: controller.OverlayErrorDismiss})
	require.Nil(t, o.err, "overlay should clear without resolving")
	require.Empty(t, replies, "dismiss must not invoke the callback")
}

func TestErrorAskHeightAccountsForWrapping(t *testing.T) {
	long := "LLM API error (400): The service ID does not exist, or the model " +
		"does not match the service. Please confirm the service ID in the console."
	st := &errorAskState{message: long}
	// At width 60 the message wraps to ~3 rows; the panel must fit
	// title + message + gap + 3 options + hint inside its borders.
	h := st.preferredAskHeight(60, 0)
	require.GreaterOrEqual(t, h, 2+1+1+3+1+len(errorOptionLabels)+1)

	// Every option row must be inside the border (row 0 and h-1 are the frame).
	o := testOverlays(controller.NewActivityHandler())
	o.err = st
	surf := o.drawError(components.DrawContext{Max: components.Size{Width: 60, Height: h}}, 60, h)
	plain := components.SurfaceText(surf)
	for _, label := range errorOptionLabels {
		require.Contains(t, plain, label)
	}
	require.Contains(t, plain, "dismiss", "hint line must survive")
}

func errorBodyText(st *errorAskState, width, height int) string {
	lines := st.visibleLines(components.DefaultTheme(), errorInnerWidth(width), height, 0)
	var b strings.Builder
	for _, line := range lines {
		for _, span := range line {
			b.WriteString(span.Text)
		}
	}
	return b.String()
}

func TestErrorAskConstrainedHeightsKeepActions(t *testing.T) {
	st := &errorAskState{message: strings.Repeat("long wrapped error ", 20)}
	o := testOverlays(controller.NewActivityHandler())
	o.err = st

	for _, height := range []int{8, 9} {
		surf := o.drawError(components.DrawContext{Max: components.Size{Width: 60, Height: height}}, 60, height)
		body := components.SurfaceText(surf)
		for _, label := range errorOptionLabels {
			require.Contains(t, body, label, "height %d must retain action %q", height, label)
		}
		require.Contains(t, body, "dismiss", "height %d must retain the hint when it fits", height)
		require.Equal(t, "│", surf.Buffer[1*surf.Size.Width+surf.Size.Width-1].Char)
	}
}

func TestErrorAskNarrowWidthWrapsActionsWithoutOverwritingBorder(t *testing.T) {
	st := &errorAskState{message: strings.Repeat("error ", 20), selected: errOptCopy}
	o := testOverlays(controller.NewActivityHandler())
	o.err = st

	for _, width := range []int{12, 8} {
		surf := o.drawError(components.DrawContext{Max: components.Size{Width: width, Height: 9}}, width, 9)
		body := errorBodyText(st, width, 9)
		require.Contains(t, body, "Copy error", "selected action must remain visible at width %d", width)
		for y := 1; y < surf.Size.Height-1; y++ {
			require.Equal(t, "│", surf.Buffer[y*surf.Size.Width+surf.Size.Width-1].Char,
				"width %d row %d right border must survive", width, y)
		}
	}
}

func TestErrorAskEmptyMessageUsesMinimumLayout(t *testing.T) {
	st := &errorAskState{}
	o := testOverlays(controller.NewActivityHandler())
	o.err = st

	require.Equal(t, 8, st.preferredAskHeight(60, 0))
	body := errorBodyText(st, 60, 8)
	for _, label := range errorOptionLabels {
		require.Contains(t, body, label)
	}
	require.Contains(t, body, "dismiss")
}

func TestErrorAskKeepsOriginalMessageForCopy(t *testing.T) {
	var replies []controller.ErrorReply
	var texts []string
	o := errorOverlays(&replies, &texts)
	original := "  error with surrounding whitespace  "
	o.beginErrorAsk(controller.OverlayMsg{ErrorText: original})
	o.acceptErrorOption(errOptCopy)

	require.Len(t, replies, 1)
	require.True(t, replies[0].Copy)
	require.Equal(t, original, texts[0])
}

func TestErrorAskReplacesOtherModals(t *testing.T) {
	var replies []controller.ErrorReply
	var texts []string
	o := errorOverlays(&replies, &texts)
	contReply := make(chan controller.ContinueReply, 1)
	o.beginContinueAsk(controller.OverlayMsg{
		Kind:      controller.OverlayContinueAsk,
		MaxRounds: 2,
		ContReply: contReply,
	})
	beginErrorAsk(o)
	require.Nil(t, o.cont, "error dialog replaces continue ask")
	require.NotNil(t, o.err)
	select {
	case <-contReply:
		// continue ask resolved as stopped
	default:
		require.Fail(t, "replaced continue ask should resolve")
	}

	// And vice versa: a permission ask resolves the error dialog as dismissed.
	permReply := make(chan controller.AskReply, 1)
	o.beginPermissionAsk(controller.OverlayMsg{
		Kind:      controller.OverlayPermissionAsk,
		Request:   permissionRequest(),
		PermReply: permReply,
	})
	require.Nil(t, o.err)
	require.Len(t, replies, 1, "replaced error dialog resolves as dismissed")
	require.False(t, replies[0].Retry)
	require.False(t, replies[0].Copy)
}
