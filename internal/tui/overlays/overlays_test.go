package overlays

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
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
