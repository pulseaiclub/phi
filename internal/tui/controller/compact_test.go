package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// drainCompactionResults empties the bus and returns compaction results, newest last.
func drainCompactionResults(bus *Bus) []CompactionFinishedMsg {
	var out []CompactionFinishedMsg
	for _, m := range bus.Drain() {
		if result, ok := m.(CompactionFinishedMsg); ok {
			out = append(out, result)
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

	var got CompactionFinishedMsg
	require.Eventually(t, func() bool {
		results := drainCompactionResults(ctrl.bus)
		if len(results) == 0 {
			return false
		}
		got = results[len(results)-1]
		return true
	}, 5*time.Second, 10*time.Millisecond)

	assert.False(t, got.Compacted)
	assert.NoError(t, got.Err)
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
