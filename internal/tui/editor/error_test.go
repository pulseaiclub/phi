package editor

import (
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func TestLoopFailureEndsStreamingBeforeDismiss(t *testing.T) {
	e := newTestEditor(t)
	e.bus.Publish(controller.SessionEventMsg{Event: session.AssistantMessageUpdate{Message: session.Message{
		ID: "partial", State: session.StateStreaming, Text: "partial output",
	}}})
	e.bus.Publish(controller.LoopFinishedMsg{ErrorText: "stream closed without assistant output"})
	e.drainBus()
	require.False(t, e.transcript.IsStreaming())
	require.True(t, e.overlays.Active())
	require.True(t, e.submitter.StreamActive(), "dialog still blocks session switches")
	e.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyEscape, Press: true})
	require.False(t, e.overlays.Active())
	require.False(t, e.submitter.IsBusy(), "dismiss releases the composer")
	require.Equal(t, "partial output", e.transcript.Snapshot().Messages[0].Text)
}

func TestCancellationDropsQueuedLoopFailure(t *testing.T) {
	e := newTestEditor(t)
	e.bus.Publish(controller.LoopFinishedMsg{ErrorText: "context canceled"})
	e.ctrl.Cancel()
	e.drainBus()
	require.False(t, e.overlays.Active())
	require.False(t, e.submitter.IsBusy())
}

func TestCancelledCompletionDoesNotShowError(t *testing.T) {
	e := newTestEditor(t)
	e.transcript.ApplySession(session.AssistantMessageUpdate{Message: session.Message{
		ID: "partial", State: session.StateStreaming, Text: "partial output",
	}})
	e.Update(controller.LoopFinishedMsg{Cancelled: true})
	require.False(t, e.overlays.Active())
	require.False(t, e.transcript.IsStreaming())
	require.Equal(t, controller.ActivityCancelled, e.activity.Current)
}

func TestRetryUnavailableRestoresIdle(t *testing.T) {
	e := newTestEditor(t)
	e.Update(controller.OverlayMsg{Kind: controller.OverlayErrorAsk, ErrorText: "error"})
	e.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	require.False(t, e.overlays.Active())
	require.False(t, e.submitter.IsBusy())
	require.Equal(t, controller.ActivityIdle, e.activity.Current)
}
