package editor

import (
	"context"
	"errors"
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/toast"
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

func TestCompactionResultToast(t *testing.T) {
	tests := []struct {
		name    string
		result  controller.CompactionFinishedMsg
		message string
		kind    toast.ToastKind
	}{
		{
			name: "committed", result: controller.CompactionFinishedMsg{Compacted: true},
			message: "Compacted context", kind: toast.ToastSuccess,
		},
		{
			name:    "committed with tokens",
			result:  controller.CompactionFinishedMsg{Compacted: true, TokensBefore: 12000},
			message: "Compacted context (was 12k tokens)", kind: toast.ToastSuccess,
		},
		{
			name:    "committed despite a late cancel",
			result:  controller.CompactionFinishedMsg{Compacted: true, Cancelled: true, TokensBefore: 2048},
			message: "Compacted context (was 2.0k tokens)", kind: toast.ToastSuccess,
		},
		{
			name:    "cancelled",
			result:  controller.CompactionFinishedMsg{Err: context.Canceled, Cancelled: true},
			message: "Compaction cancelled", kind: toast.ToastWarning,
		},
		{
			name: "cancelled by context alone", result: controller.CompactionFinishedMsg{Cancelled: true},
			message: "Compaction cancelled", kind: toast.ToastWarning,
		},
		{
			name: "failed", result: controller.CompactionFinishedMsg{Err: errors.New("boom")},
			message: "Compaction failed: boom", kind: toast.ToastError,
		},
		{
			name: "nothing to do", result: controller.CompactionFinishedMsg{},
			message: "Nothing to compact yet", kind: toast.ToastWarning,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTestEditor(t)
			e.Update(tt.result)
			assert.Equal(t, tt.message, e.toast.Message)
			assert.Equal(t, tt.kind, e.toast.Kind)
			assert.True(t, e.toast.Visible())
		})
	}
}
