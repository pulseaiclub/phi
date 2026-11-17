package editor

import (
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func TestOverlayCancellationRoutes(t *testing.T) {
	for _, kind := range []struct {
		name string
		kind controller.OverlayKind
	}{
		{"confirm", controller.OverlayExtConfirm},
		{"error", controller.OverlayErrorAsk},
	} {
		for _, route := range []string{"escape", "cancel"} {
			t.Run(kind.name+"/"+route, func(t *testing.T) {
				e := newTestEditor(t)
				confirmReply := make(chan controller.ExtConfirmReply, 1)
				e.Update(controller.OverlayMsg{
					Kind: kind.kind, ConfirmReply: confirmReply, ErrorText: "failed",
				})
				require.True(t, e.submitter.StreamActive())

				if route == "escape" {
					e.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyEscape, Press: true})
				} else {
					e.Update(controller.CancelStreamMsg{})
				}
				require.False(t, e.overlays.Active())
				if route == "cancel" {
					require.Equal(t, controller.ActivityCancelled, e.activity.Current)
				}
				switch kind.kind {
				case controller.OverlayExtConfirm:
					require.Len(t, confirmReply, 1, "cancellation must unblock the confirmation waiter")
					require.Equal(t, controller.ExtConfirmReply{}, <-confirmReply)
				case controller.OverlayErrorAsk:
					require.False(t, e.submitter.StreamActive())
				}
			})
		}
	}
}
