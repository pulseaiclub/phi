package overlays

import (
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func testOverlays(activity *controller.ActivityHandler) *Overlays {
	return NewOverlays(components.DefaultTheme(), activity, nil, nil, nil)
}

func TestResolvePermissionSendsReply(t *testing.T) {
	activity := controller.NewActivityHandler(nil)
	o := testOverlays(activity)
	reply := make(chan controller.AskReply, 1)
	o.beginPermissionAsk(controller.OverlayMsg{
		Kind:      controller.OverlayPermissionAsk,
		Request:   permission.Request{Kind: permission.KindExec, Tool: "bash", Command: "curl x"},
		Reason:    "needs approval",
		PermReply: reply,
	})
	require.NotNil(t, o.perm, "expected permAsk")
	require.Equal(t, "Run this command?", o.perm.header)
	require.Equal(t, controller.ActivityAwaitingApproval, activity.Current)
	o.resolvePermission(controller.AskReply{Approved: true})
	require.Nil(t, o.perm, "expected cleared")
	select {
	case r := <-reply:
		require.True(t, r.Approved, "want approved")
	default:
		require.Fail(t, "expected reply")
	}
}

func TestPermissionDenyWithFeedback(t *testing.T) {
	o := testOverlays(controller.NewActivityHandler(nil))
	reply := make(chan controller.AskReply, 1)
	o.beginPermissionAsk(controller.OverlayMsg{
		Kind:      controller.OverlayPermissionAsk,
		Request:   permission.Request{Tool: "bash", Kind: permission.KindExec, Command: "curl https://x"},
		PermReply: reply,
	})
	o.acceptPermissionOption(askOptDenyFeedback)
	require.True(t, o.perm != nil && o.perm.feedbackMode, "expected feedback mode")
	o.perm.feedback = "use docs instead"
	o.resolvePermission(controller.AskReply{Feedback: o.perm.feedback})
	r := <-reply
	require.False(t, r.Approved)
	require.Equal(t, "use docs instead", r.Feedback)
}

func TestPermissionDismissClearsOverlay(t *testing.T) {
	o := testOverlays(controller.NewActivityHandler(nil))
	reply := make(chan controller.AskReply, 1)
	o.beginPermissionAsk(controller.OverlayMsg{
		Kind:      controller.OverlayPermissionAsk,
		Request:   permission.Request{Tool: "bash", Kind: permission.KindExec, Command: "curl https://x"},
		PermReply: reply,
	})
	o.Apply(controller.OverlayMsg{Kind: controller.OverlayPermissionDismiss})
	require.Nil(t, o.perm, "overlay should clear without consuming reply")
	select {
	case <-reply:
		require.Fail(t, "dismiss must not send on reply")
	default:
	}
}

func TestDrawPermissionAskReplacesComposerSlot(t *testing.T) {
	o := testOverlays(controller.NewActivityHandler(nil))
	reply := make(chan controller.AskReply, 1)
	o.beginPermissionAsk(controller.OverlayMsg{
		Kind:      controller.OverlayPermissionAsk,
		Request:   permission.Request{Kind: permission.KindExec, Tool: "bash", Command: "rm -f todo.list"},
		Reason:    "Matches built-in permissions rule",
		PermReply: reply,
	})
	surf := o.drawPermissionAsk(components.DrawContext{
		Max:    components.Size{Width: 60, Height: 12},
		Method: 0,
	}, 60, 12)
	require.Equal(t, 60, surf.Size.Width)
	require.Equal(t, 12, surf.Size.Height)
}

func TestFormatAskHeader(t *testing.T) {
	h, d := formatAskHeader(permission.Request{Kind: permission.KindWrite, Paths: []string{"/tmp/a"}})
	require.Equal(t, "Allow creating file:", h)
	require.Equal(t, "/tmp/a", d)
}

func TestContinueAskResolveContinue(t *testing.T) {
	activity := controller.NewActivityHandler(nil)
	o := testOverlays(activity)
	reply := make(chan controller.ContinueReply, 1)
	o.beginContinueAsk(controller.OverlayMsg{
		Kind:      controller.OverlayContinueAsk,
		MaxRounds: 64,
		ContReply: reply,
	})
	require.NotNil(t, o.cont, "expected continueAsk")
	require.Equal(t, 64, o.cont.maxRounds)
	require.Equal(t, controller.ActivityAwaitingApproval, activity.Current)
	o.resolveContinue(controller.ContinueReply{Continue: true})
	require.Nil(t, o.cont, "expected continueAsk cleared")
	select {
	case r := <-reply:
		require.True(t, r.Continue, "expected Continue=true")
	default:
		require.Fail(t, "expected reply")
	}
}

func TestContinueAskEscapeStops(t *testing.T) {
	o := testOverlays(controller.NewActivityHandler(nil))
	reply := make(chan controller.ContinueReply, 1)
	o.beginContinueAsk(controller.OverlayMsg{
		Kind:      controller.OverlayContinueAsk,
		MaxRounds: 2,
		ContReply: reply,
	})
	ctx := &components.EventContext{}
	_ = o.handleContinueKey(ctx, xui.KeyEvent{Press: true, Code: xui.KeyEscape})
	select {
	case r := <-reply:
		require.False(t, r.Continue, "escape should stop")
	default:
		require.Fail(t, "expected reply on escape")
	}
}

func TestContinueDismissClearsOverlay(t *testing.T) {
	o := testOverlays(controller.NewActivityHandler(nil))
	reply := make(chan controller.ContinueReply, 1)
	o.beginContinueAsk(controller.OverlayMsg{
		Kind:      controller.OverlayContinueAsk,
		MaxRounds: 2,
		ContReply: reply,
	})
	o.Apply(controller.OverlayMsg{Kind: controller.OverlayContinueDismiss})
	require.Nil(t, o.cont, "overlay should clear without consuming reply")
	select {
	case <-reply:
		require.Fail(t, "dismiss must not send on reply")
	default:
	}
}
