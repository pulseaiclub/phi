package overlay

import (
	"strings"
	"testing"
	"time"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/layout"
	"github.com/pulseaiclub/phi/internal/components/status"
	uistoast "github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/plugin/activity"
)

const testErrText = "request ended without sending any chunks"

// resolveRecorder observes the reply side effects applyReply performs.
type resolveRecorder struct {
	retryCalls int
	copied     []string
	toasts     []string
}

func (r *resolveRecorder) empty() bool {
	return r.retryCalls == 0 && len(r.copied) == 0 && len(r.toasts) == 0
}

type recordingToast struct{ rec *resolveRecorder }

func (t *recordingToast) Show(message string, _ uistoast.ToastKind, _ time.Duration) {
	t.rec.toasts = append(t.rec.toasts, message)
}

// testActivity builds a real activity plugin over a no-op status slot.
func testActivity() *activity.Plugin {
	th := components.DefaultTheme()
	return activity.New(th, status.NewSpinner(th.ToolName), activity.Deps{
		Snapshot:      func() session.Snapshot { return session.Snapshot{} },
		ContextWindow: func() int { return 0 },
		Labels:        nopLabels{},
	})
}

type nopLabels struct{}

func (nopLabels) SetBottomLeftLabel(layout.BorderLabel) {}
func (nopLabels) ClearBottomLeftLabel()                 {}

// errorPlugin builds overlays whose reply side effects record into rec.
func errorPlugin(rec *resolveRecorder) *Plugin {
	return New(Deps{
		Theme:    components.DefaultTheme(),
		Activity: testActivity(),
		Retry:    func() bool { rec.retryCalls++; return true },
		Copy:     func(text string) bool { rec.copied = append(rec.copied, text); return true },
		Toast:    &recordingToast{rec: rec},
	})
}

func beginErrorAsk(o *Plugin) {
	o.beginErrorAsk(controller.OverlayMsg{Kind: controller.OverlayErrorAsk, ErrorText: testErrText})
}

func TestErrorAskRendersAndResolves(t *testing.T) {
	rec := &resolveRecorder{}
	o := errorPlugin(rec)
	beginErrorAsk(o)
	require.NotNil(t, o.err, "expected errorAsk")
	require.True(t, o.Active(), "error dialog must block the composer")

	surf := o.drawError(components.DrawContext{Max: components.Size{Width: 60, Height: 12}}, 60, 12)
	require.Equal(t, 60, surf.Size.Width)
	require.Equal(t, 12, surf.Size.Height)

	h, overlay := o.PreferredHeight(60, 0)
	require.True(t, overlay)
	require.GreaterOrEqual(t, h, 8)

	o.acceptErrorOption(errOptRetry)
	require.Nil(t, o.err, "expected cleared")
	require.Equal(t, 1, rec.retryCalls)
	assert.Empty(t, rec.copied)
	assert.Empty(t, rec.toasts, "a successful retry shows no toast")
}

func TestErrorAskKeyNavigation(t *testing.T) {
	rec := &resolveRecorder{}
	o := errorPlugin(rec)
	beginErrorAsk(o)
	ctx := &components.EventContext{}

	// Down ×2 → Dismiss; Up wraps back to Copy.
	o.handleErrorKey(ctx, xui.KeyEvent{Press: true, Code: xui.KeyDown})
	o.handleErrorKey(ctx, xui.KeyEvent{Press: true, Code: xui.KeyDown})
	require.Equal(t, errOptDismiss, o.err.selected)
	o.handleErrorKey(ctx, xui.KeyEvent{Press: true, Code: xui.KeyUp})
	require.Equal(t, errOptCopy, o.err.selected)

	// Enter on Copy copies the original error text.
	o.handleErrorKey(ctx, xui.KeyEvent{Press: true, Code: xui.KeyEnter})
	require.Equal(t, []string{testErrText}, rec.copied)
	require.Zero(t, rec.retryCalls)
	require.Nil(t, o.err)
}

func TestErrorAskEscapeDismisses(t *testing.T) {
	rec := &resolveRecorder{}
	o := errorPlugin(rec)
	beginErrorAsk(o)
	ctx := &components.EventContext{}
	o.handleErrorKey(ctx, xui.KeyEvent{Press: true, Code: xui.KeyEscape})
	require.Nil(t, o.err, "escape should resolve")
	require.True(t, rec.empty(), "escape resolves without side effects")
}

func TestErrorDismissClearsOverlay(t *testing.T) {
	rec := &resolveRecorder{}
	o := errorPlugin(rec)
	beginErrorAsk(o)
	o.Apply(controller.OverlayMsg{Kind: controller.OverlayErrorDismiss})
	require.Nil(t, o.err, "overlay should clear without resolving")
	require.True(t, rec.empty(), "dismiss must not trigger side effects")
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
	o := testPlugin(testActivity())
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
	o := testPlugin(testActivity())
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
	o := testPlugin(testActivity())
	o.err = st

	for _, width := range []int{12, 8} {
		surf := o.drawError(components.DrawContext{Max: components.Size{Width: width, Height: 9}}, width, 9)
		body := errorBodyText(st, width, 9)
		require.Contains(t, body, "Copy to Clipboard", "selected action must remain visible at width %d", width)
		for y := 1; y < surf.Size.Height-1; y++ {
			require.Equal(t, "│", surf.Buffer[y*surf.Size.Width+surf.Size.Width-1].Char,
				"width %d row %d right border must survive", width, y)
		}
	}
}

func TestErrorAskEmptyMessageUsesMinimumLayout(t *testing.T) {
	st := &errorAskState{}
	o := testPlugin(testActivity())
	o.err = st

	require.Equal(t, 8, st.preferredAskHeight(60, 0))
	body := errorBodyText(st, 60, 8)
	for _, label := range errorOptionLabels {
		require.Contains(t, body, label)
	}
	require.Contains(t, body, "dismiss")
}

func TestErrorAskKeepsOriginalMessageForCopy(t *testing.T) {
	rec := &resolveRecorder{}
	o := errorPlugin(rec)
	original := "  error with surrounding whitespace  "
	o.beginErrorAsk(controller.OverlayMsg{ErrorText: original})
	o.acceptErrorOption(errOptCopy)

	require.Equal(t, []string{original}, rec.copied, "copy gets the original, untrimmed text")
}
