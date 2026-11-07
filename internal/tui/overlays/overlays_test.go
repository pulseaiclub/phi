package overlays

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func testOverlays(activity *controller.ActivityHandler) *Overlays {
	return NewOverlays(components.DefaultTheme(), activity, nil, nil, nil, nil)
}

func TestConfirmRestoresPreviousActivity(t *testing.T) {
	for _, previous := range []controller.Activity{controller.ActivityIdle, controller.ActivityTools} {
		activity := controller.NewActivityHandler()
		activity.Apply(previous)
		o := testOverlays(activity)
		reply := make(chan controller.ExtConfirmReply, 1)
		request := controller.OverlayMsg{Kind: controller.OverlayExtConfirm, ConfirmReply: reply}
		o.beginExtConfirm(request)
		require.Equal(t, controller.ActivityAwaitingApproval, activity.Current)
		o.resolveExtConfirm(controller.ExtConfirmReply{OK: true})
		require.Equal(t, previous, activity.Current)
		require.True(t, (<-reply).OK)
		o.beginExtConfirm(request)
		o.dismissExtConfirm()
		require.Equal(t, previous, activity.Current)
		require.Empty(t, reply, "timeout dismissal must not send a reply")
	}
}

func TestConfirmPreservesUpdatedActivity(t *testing.T) {
	for _, action := range []string{"reply", "dismiss"} {
		t.Run(action, func(t *testing.T) {
			activity := controller.NewActivityHandler()
			o := testOverlays(activity)
			o.beginExtConfirm(controller.OverlayMsg{Kind: controller.OverlayExtConfirm})
			activity.Apply(controller.ActivityCancelled)
			if action == "reply" {
				o.resolveExtConfirm(controller.ExtConfirmReply{OK: true})
			} else {
				o.dismissExtConfirm()
			}
			require.Equal(t, controller.ActivityCancelled, activity.Current)
		})
	}
}

func TestResolvePermissionSendsReply(t *testing.T) {
	activity := controller.NewActivityHandler()
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
	o := testOverlays(controller.NewActivityHandler())
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
	o := testOverlays(controller.NewActivityHandler())
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
	o := testOverlays(controller.NewActivityHandler())
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
