package agent_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/tools/agenttool"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

func TestBindingUsesParentWorkDir(t *testing.T) {
	workDir := t.TempDir()
	var got job.Meta
	mgr, err := job.New(job.Options{
		Root: t.TempDir(),
		Runner: job.RunnerFunc(func(_ context.Context, env job.RunEnv) (string, error) {
			got = env.Job
			return "ok", nil
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close(t.Context()) })

	reg := tooldef.NewRegistry(agenttool.Bind(mgr, "parent-1", workDir).Tools())
	res, err := reg["agent_spawn"].Run(t.Context(), []byte(`{"prompt":"inspect","workdir":"child"}`))
	require.NoError(t, err)
	var spawned struct {
		JobID string `json:"job_id"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.Content), &spawned))
	_, err = reg["agent_wait"].Run(t.Context(), []byte(`{"job_id":"`+spawned.JobID+`"}`))
	require.NoError(t, err)
	assert.Equal(t, "parent-1", got.ParentID)
	assert.Equal(t, filepath.Join(workDir, "child"), got.WorkDir)
}

func TestChildToolsHaveNoAgent(t *testing.T) {
	for _, tool := range agent.SpecForRole(job.RoleExplore).Tools {
		assert.NotContains(t, tool.Definition.Name, "agent_")
	}
}

func TestChildToolsAreReadonly(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range agent.SpecForRole(job.RoleExplore).Tools {
		names[tool.Definition.Name] = true
	}
	assert.True(t, names["read"])
	assert.True(t, names["grep"])
	assert.True(t, names["bash"])
	assert.False(t, names["write"])
	assert.False(t, names["edit"])
}
