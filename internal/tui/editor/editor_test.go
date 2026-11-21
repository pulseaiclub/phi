package editor

import (
	"testing"
	"time"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// recordingComposer captures palette rebuilds so a test can see that the slash
// surface was re-derived.
type recordingComposer struct {
	updates int
	last    []palette.PaletteCommand
}

func (r *recordingComposer) SetPaletteCommands(cmds []palette.PaletteCommand) {
	r.updates++
	r.last = cmds
}

// Extensions can change underneath the palette (a reload, an extension unloading
// itself). The palette has to be re-derived, or it keeps advertising commands
// whose process is already gone.
func TestExtTopologyChangedResyncsSlashSurface(t *testing.T) {
	e := newTestEditor(t)
	require.NotNil(t, e.extCmds)

	rec := &recordingComposer{}
	e.extCmds.Composer = rec

	e.bus.Publish(controller.ExtTopologyChangedMsg{})
	e.drainBus()

	require.Equal(t, 1, rec.updates, "the palette must be rebuilt from the loaded extensions")
	assert.NotEmpty(t, rec.last, "the builtin surface survives the resync")
}

func TestShellCompletionWithoutHistory(t *testing.T) {
	e := newTestEditor(t)
	require.Empty(t, e.ctrl.SessionDir())
	e.composer.Chat.Value = "!git ch"
	e.composer.Chat.Cursor = len(e.composer.Chat.Value)
	e.composer.Chat.OnBashChange(true, "git ch")
	t.Cleanup(e.composer.HideCompleters)

	var result controller.BashSuggestionsMsg
	require.Eventually(t, func() bool {
		for _, msg := range e.bus.Drain() {
			if suggestions, ok := msg.(controller.BashSuggestionsMsg); ok {
				result = suggestions
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond)
	require.Empty(t, result.ErrText)
	require.NotEmpty(t, result.Items)
	assert.Contains(t, result.Items[0].Command, "git ch")
	e.Update(result)
	assert.True(t, e.composer.Chat.BashOpen)
}

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

func TestSpinnerAdvancesEveryFourthFrame(t *testing.T) {
	e := newTestEditor(t)
	e.activity.Apply(controller.ActivityStreaming)

	for range spinnerFrameDivisor - 1 {
		frame(t, e)
	}
	require.Equal(t, 0, e.spin.Frame)

	frame(t, e)
	require.Equal(t, 1, e.spin.Frame)

	e.activity.Apply(controller.ActivityCancelled)
	require.Equal(t, 0, e.spin.Frame, "activity changes reset the frame")
	for range spinnerFrameDivisor {
		frame(t, e)
	}
	require.Equal(t, 0, e.spin.Frame, "activities without spinner must not advance frames")
}

func TestActivityChangeRestartsSpinnerPace(t *testing.T) {
	e := newTestEditor(t)
	e.activity.Apply(controller.ActivityStreaming)
	for range 2 {
		e.advanceSpinner()
	}

	e.activity.Apply(controller.ActivityTools)
	require.Equal(t, 0, e.spin.Frame)
	require.Equal(t, 0, e.spinTick)

	for range spinnerFrameDivisor {
		e.advanceSpinner()
	}
	require.Equal(t, 1, e.spin.Frame)
}
