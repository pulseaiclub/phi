package editor

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func TestConfirmActivityExpiry(t *testing.T) {
	for _, tc := range []struct {
		name     string
		previous controller.Activity
		current  controller.Activity
		want     controller.Activity
	}{
		{"expired cancellation", controller.ActivityCancelled, controller.ActivityAwaitingApproval, controller.ActivityIdle},
		{"unrelated expiry", controller.ActivityTools, controller.ActivityAwaitingApproval, controller.ActivityTools},
		{"new activity", controller.ActivityCancelled, controller.ActivityStreaming, controller.ActivityStreaming},
	} {
		for _, action := range []string{"reply", "dismiss"} {
			t.Run(tc.name+"/"+action, func(t *testing.T) {
				e := newTestEditor(t)
				e.activity.Apply(tc.previous)
				e.Update(controller.OverlayMsg{Kind: controller.OverlayExtConfirm})
				if tc.current != controller.ActivityAwaitingApproval {
					e.activity.Apply(tc.current)
				}
				e.Update(controller.FooterMsg{
					Kind: controller.FooterClearIfActivity, If: controller.ActivityCancelled,
				})
				require.True(t, e.overlays.ConfirmActive())
				require.Equal(t, tc.current, e.activity.Current, "expiry must preserve the visible activity")
				if action == "reply" {
					e.overlays.ResolveConfirm(controller.ExtConfirmReply{OK: true})
				} else {
					e.Update(controller.OverlayMsg{Kind: controller.OverlayExtConfirmDismiss})
				}
				require.Equal(t, tc.want, e.activity.Current)
			})
		}
	}
}

func TestActivityExpiresAfterConfirmCloses(t *testing.T) {
	for _, action := range []string{"reply", "dismiss"} {
		t.Run(action, func(t *testing.T) {
			e := newTestEditor(t)
			e.activity.Apply(controller.ActivityCancelled)
			e.Update(controller.OverlayMsg{Kind: controller.OverlayExtConfirm})
			if action == "reply" {
				e.overlays.ResolveConfirm(controller.ExtConfirmReply{OK: true})
			} else {
				e.Update(controller.OverlayMsg{Kind: controller.OverlayExtConfirmDismiss})
			}
			require.Equal(t, controller.ActivityCancelled, e.activity.Current)
			e.Update(controller.FooterMsg{
				Kind: controller.FooterClearIfActivity, If: controller.ActivityCancelled,
			})
			require.Equal(t, controller.ActivityIdle, e.activity.Current)
		})
	}
}
