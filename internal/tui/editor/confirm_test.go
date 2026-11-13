package editor

import (
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func TestConfirmActivityExpiry(t *testing.T) {
	for _, action := range []string{"reply", "dismiss"} {
		t.Run(action, func(t *testing.T) {
			e := newTestEditor(t)
			e.activity.Apply(controller.ActivityCancelled)
			e.Update(controller.OverlayMsg{Kind: controller.OverlayExtConfirm})
			e.Update(controller.FooterMsg{
				Kind: controller.FooterClearIfActivity, If: controller.ActivityCancelled,
			})
			require.Equal(t, controller.ActivityAwaitingApproval, e.activity.Current)
			if action == "reply" {
				e.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
			} else {
				e.Update(controller.OverlayMsg{Kind: controller.OverlayExtConfirmDismiss})
			}
			require.Equal(t, controller.ActivityIdle, e.activity.Current)
		})
	}
}
