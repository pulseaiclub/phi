package controller

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	assert.Equal(t, "Nothing to summarize yet", got.Message)
}

// A second /compact must be refused while the first is still running: both
// would cut the same leaf and the loser would abort on the anchor check.
func TestCompactRefusesConcurrentRun(t *testing.T) {
	ctrl := newTestController(t, "http://127.0.0.1:1")
	ctrl.streamMu.Lock()
	ctrl.compactCancel = func() {}
	ctrl.streamMu.Unlock()

	err := ctrl.Compact()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already running")
}

// Esc during a summary cancels it, so it cannot keep compacting a session the
// user is about to switch away from.
func TestCompactIsCancelledByCancel(t *testing.T) {
	ctrl := newTestController(t, "http://127.0.0.1:1")
	cancelled := false
	ctrl.streamMu.Lock()
	ctrl.compactCancel = func() { cancelled = true }
	ctrl.streamMu.Unlock()

	ctrl.Cancel()
	assert.True(t, cancelled)
}
