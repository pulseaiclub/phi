package job_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/job"
)

func TestSpawnPassesIndependentParentContexts(t *testing.T) {
	ready := make(chan job.RunEnv, 4)
	release := make(chan struct{})
	mgr := newMgr(t, job.RunnerFunc(func(ctx context.Context, env job.RunEnv) (string, error) {
		ready <- env
		select {
		case <-release:
			return "done", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}), job.Options{MaxConcurrent: 4})
	t.Cleanup(func() { close(release) })

	contexts := []job.ParentContext{
		{},
		{Mode: job.ContextModeClean},
		{Mode: job.ContextModeHandoff, Text: "First task constraints"},
		{Mode: job.ContextModeHandoff, Text: "Second task constraints"},
	}
	want := make(map[string]job.ParentContext)
	for _, parent := range contexts {
		req := job.SpawnRequest{Prompt: "work", ParentContext: parent}
		info, err := mgr.Spawn(t.Context(), req)
		require.NoError(t, err)
		want[info.ID] = parent
		// A caller may reuse its request after Spawn returns.
		req.ParentContext.Mode = job.ContextModeClean
		req.ParentContext.Text = "later change"
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for range contexts {
		select {
		case env := <-ready:
			expected, ok := want[env.Job.ID]
			require.True(t, ok)
			assert.Equal(t, expected, env.ParentContext)
			delete(want, env.Job.ID)
		case <-deadline.C:
			require.FailNow(t, "jobs did not reach the runner before the deadline")
		}
	}
	assert.Empty(t, want)
}

func TestSpawnRejectsInvalidParentContextBeforeCreatingJob(t *testing.T) {
	root := t.TempDir()
	calls := make(chan struct{}, 1)
	mgr := newMgr(t, job.RunnerFunc(func(_ context.Context, _ job.RunEnv) (string, error) {
		calls <- struct{}{}
		return "done", nil
	}), job.Options{Root: root, MaxConcurrent: 1})

	_, err := mgr.Spawn(t.Context(), job.SpawnRequest{
		Prompt: "work", ParentContext: job.ParentContext{Mode: "fork"},
	})
	require.ErrorIs(t, err, job.ErrInvalid)
	assert.Contains(t, err.Error(), "clean|handoff")
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Empty(t, entries)
	assert.Empty(t, calls)

	// A rejected request must not consume the only concurrency slot.
	info, err := mgr.Spawn(t.Context(), job.SpawnRequest{Prompt: "valid work"})
	require.NoError(t, err)
	result, err := mgr.Wait(t.Context(), info.ID)
	require.NoError(t, err)
	assert.Equal(t, job.StatusCompleted, result.Info.Status)
}
