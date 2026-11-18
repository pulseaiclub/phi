package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components/toast"
)

// drainToasts empties the bus and returns the toast messages, newest last.
func drainToasts(bus *Bus) []ToastMsg {
	var out []ToastMsg
	for _, m := range bus.Drain() {
		if tm, ok := m.(ToastMsg); ok {
			out = append(out, tm)
		}
	}
	return out
}

// /compact on a fresh session has nothing to walk. It must report that state
// rather than a success that reclaimed nothing (and must not panic in the cut
// planner, which indexes the first retained entry).
func TestCompactReportsNothingToCompact(t *testing.T) {
	ctrl := newTestController(t, "http://127.0.0.1:1")

	require.NoError(t, ctrl.Compact())

	var got ToastMsg
	require.Eventually(t, func() bool {
		toasts := drainToasts(ctrl.bus)
		if len(toasts) == 0 {
			return false
		}
		got = toasts[len(toasts)-1]
		return true
	}, 5*time.Second, 10*time.Millisecond)

	assert.Equal(t, "Nothing to compact yet", got.Message)
}

// The slot is claimed for as long as a summary runs: a second /compact must be
// refused, because both would cut the same leaf and the loser would abort on
// the anchor check. Releasing the slot must accept the next one.
func TestBeginCompactionRefusesConcurrentRun(t *testing.T) {
	ctrl := newTestController(t, "http://127.0.0.1:1")

	_, ok := ctrl.beginCompaction()
	require.True(t, ok, "the first run claims the slot")

	_, ok = ctrl.beginCompaction()
	require.False(t, ok, "a second run must be refused while the first holds the slot")

	ctrl.endCompaction()
	_, ok = ctrl.beginCompaction()
	require.True(t, ok, "releasing the slot accepts the next run")
	ctrl.endCompaction()
}

// Esc during a compaction cancels the context the summary worker runs on, so it
// cannot keep compacting a session the user is about to switch away from.
func TestCancelCancelsRunningCompaction(t *testing.T) {
	ctrl := newTestController(t, "http://127.0.0.1:1")

	ctx, ok := ctrl.beginCompaction()
	require.True(t, ok)
	require.NoError(t, ctx.Err())

	ctrl.Cancel()
	require.ErrorIs(t, ctx.Err(), context.Canceled, "Cancel must reach the compaction context")
	ctrl.endCompaction()
}

// compactionToast is the whole outcome mapping. The committed-checkpoint row
// pins the late-cancel case: Esc after AppendCompaction succeeded must not
// report "cancelled" while the transcript already shows the summary.
func TestCompactionToast(t *testing.T) {
	tests := []struct {
		name    string
		did     bool
		err     error
		ctxErr  error
		before  int
		message string
		kind    toast.ToastKind
	}{
		{name: "committed", did: true, before: 0, message: "Compacted context", kind: toast.ToastSuccess},
		{
			name: "committed with tokens", did: true, before: 12000, message: "Compacted context (was 12k tokens)",
			kind: toast.ToastSuccess,
		},
		{
			name: "committed despite a late cancel", did: true, ctxErr: context.Canceled, before: 2048,
			message: "Compacted context (was 2.0k tokens)", kind: toast.ToastSuccess,
		},
		{
			name: "cancelled", err: context.Canceled, ctxErr: context.Canceled,
			message: "Compaction cancelled", kind: toast.ToastWarning,
		},
		{
			name: "cancelled by ctx alone", ctxErr: context.Canceled,
			message: "Compaction cancelled", kind: toast.ToastWarning,
		},
		{name: "failed", err: errors.New("boom"), message: "Compaction failed: boom", kind: toast.ToastError},
		{name: "nothing to do", message: "Nothing to compact yet", kind: toast.ToastWarning},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compactionToast(tt.did, tt.err, tt.ctxErr, tt.before)
			assert.Equal(t, tt.message, got.Message)
			assert.Equal(t, tt.kind, got.Kind)
		})
	}
}
