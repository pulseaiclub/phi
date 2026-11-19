package job_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/job"
)

func TestSpawnSnapshotsParentAndResolvesWorkDir(t *testing.T) {
	ready := make(chan job.RunEnv, 1)
	release := make(chan struct{})
	mgr := newMgr(t, t.TempDir(), job.RunnerFunc(func(ctx context.Context, env job.RunEnv) (string, error) {
		ready <- env
		select {
		case <-release:
			return "done", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}), job.WithMaxDepth(2), job.WithParent("session-old", "/workspace/old", 1))

	info, err := mgr.Spawn(t.Context(), job.SpawnRequest{Prompt: "default workdir"})
	require.NoError(t, err)
	mgr.SetParent("session-new", "/workspace/new", 0)

	env := <-ready
	assert.Equal(t, "session-old", env.Job.ParentID)
	assert.Equal(t, 1, env.Job.ParentDepth)
	assert.Equal(t, "/workspace/old", env.Job.WorkDir)

	close(release)
	_, err = mgr.Wait(t.Context(), info.ID)
	require.NoError(t, err)
}
