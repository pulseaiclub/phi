package agenttool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/tools"
	"github.com/pulseaiclub/phi/internal/tools/agenttool"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

func TestAgentToolsSpawnWaitForcesDepthAndParent(t *testing.T) {
	mgr, err := job.New(job.Options{
		Root: t.TempDir(),
		Runner: job.RunnerFunc(func(_ context.Context, env job.RunEnv) (string, error) {
			assert.Equal(t, "parent-1", env.Job.ParentID)
			assert.Equal(t, 0, env.Job.ParentDepth)
			return "summary-ok", nil
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close(t.Context()) })

	reg := tooldef.NewRegistry(agenttool.AgentTools(agenttool.AgentDeps{
		Manager:  mgr,
		ParentID: func() string { return "parent-1" },
		WorkDir:  func() string { return t.TempDir() },
	}))
	require.Contains(t, reg, "agent_spawn")
	require.Contains(t, reg, "agent_wait")
	require.NotContains(t, tooldef.NewRegistry(tools.DefaultTools(nil)), "agent_spawn")

	spawnArgs, _ := json.Marshal(map[string]any{
		"prompt":      "do work",
		"description": "d",
		"depth":       99, // must be ignored
		"parent_id":   "evil",
	})
	spawnRes, err := reg["agent_spawn"].Run(t.Context(), spawnArgs)
	require.NoError(t, err)
	assert.Contains(t, spawnRes.Content, `"role": "explore"`)

	var spawned struct {
		JobID string `json:"job_id"`
	}
	require.NoError(t, json.Unmarshal([]byte(spawnRes.Content), &spawned))
	assert.Equal(t, "explore: d", spawnRes.Detail)

	waitArgs, _ := json.Marshal(map[string]any{"job_id": spawned.JobID})
	waitRes, err := reg["agent_wait"].Run(t.Context(), waitArgs)
	require.NoError(t, err)
	assert.Contains(t, waitRes.Content, "summary-ok")
	assert.Contains(t, waitRes.Content, `"status": "completed"`)
	assert.Equal(t, "explore: d", waitRes.Detail)
}

func TestAgentToolsSpawnRoleWorker(t *testing.T) {
	mgr, err := job.New(job.Options{
		Root: t.TempDir(),
		Runner: job.RunnerFunc(func(_ context.Context, env job.RunEnv) (string, error) {
			assert.Equal(t, job.RoleWorker, env.Job.Role)
			return "done", nil
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close(t.Context()) })

	reg := tooldef.NewRegistry(agenttool.AgentTools(agenttool.AgentDeps{
		Manager:  mgr,
		ParentID: func() string { return "p" },
		WorkDir:  func() string { return t.TempDir() },
	}))
	raw, _ := json.Marshal(map[string]any{
		"prompt": "implement x",
		"role":   "worker",
	})
	assert.Equal(t, "worker: implement x", reg["agent_spawn"].DetailFromArgs(raw))

	res, err := reg["agent_spawn"].Run(t.Context(), raw)
	require.NoError(t, err)
	assert.Contains(t, res.Content, `"role": "worker"`)

	var spawned struct {
		JobID string `json:"job_id"`
		Label string `json:"label"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.Content), &spawned))
	require.NotEmpty(t, spawned.JobID)
	// The row keeps the caller's label; a finished job must not title itself
	// with its job id.
	assert.Equal(t, "worker: implement x", res.Detail)
	assert.Equal(t, "worker: implement x", spawned.Label)
	assert.NotContains(t, res.Detail, spawned.JobID)

	waitArgs, _ := json.Marshal(map[string]any{"job_id": spawned.JobID})
	waitRes, err := reg["agent_wait"].Run(t.Context(), waitArgs)
	require.NoError(t, err)
	assert.Contains(t, waitRes.Content, `"status": "completed"`)
	assert.Equal(t, "worker: implement x", waitRes.Detail)
}

func TestAgentSpawnDetailAlwaysIncludesRole(t *testing.T) {
	mgr, err := job.New(job.Options{
		Root: t.TempDir(),
		Runner: job.RunnerFunc(func(_ context.Context, _ job.RunEnv) (string, error) {
			return "x", nil
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close(t.Context()) })

	reg := tooldef.NewRegistry(agenttool.AgentTools(agenttool.AgentDeps{
		Manager:  mgr,
		ParentID: func() string { return "p" },
		WorkDir:  func() string { return t.TempDir() },
	}))
	detail := reg["agent_spawn"].DetailFromArgs
	require.NotNil(t, detail)

	assert.Equal(t, "explore: find auth", detail([]byte(`{"prompt":"p","description":"find auth"}`)))
	assert.Equal(t, "explore: do the thing", detail([]byte(`{"prompt":"do the thing"}`)))
	assert.Equal(t, "review: check diff", detail([]byte(`{"prompt":"p","description":"check diff","role":"review"}`)))
	// Multi-line prompts collapse: the label has to stay one TUI row.
	assert.Equal(t, "explore: find the auth config",
		detail([]byte(`{"prompt":"find the\n   auth config"}`)))
}

func TestAgentToolsSpawnBadRole(t *testing.T) {
	mgr, err := job.New(job.Options{
		Root: t.TempDir(),
		Runner: job.RunnerFunc(func(_ context.Context, _ job.RunEnv) (string, error) {
			return "x", nil
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close(t.Context()) })

	reg := tooldef.NewRegistry(agenttool.AgentTools(agenttool.AgentDeps{
		Manager: mgr,
	}))
	raw, _ := json.Marshal(map[string]any{"prompt": "x", "role": "nope"})
	_, err = reg["agent_spawn"].Run(t.Context(), raw)
	require.Error(t, err)
}

func TestChildToolsExcludeAgent(t *testing.T) {
	for _, tool := range tools.DefaultTools(nil) {
		assert.NotContains(t, tool.Definition.Name, "agent_")
	}
}
