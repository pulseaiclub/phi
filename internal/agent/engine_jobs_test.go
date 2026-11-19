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
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/tools/agenttool"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

func TestNewEngineRegistersJobs(t *testing.T) {
	mgr, err := job.New(job.Options{
		Root: t.TempDir(),
		Runner: job.RunnerFunc(func(_ context.Context, _ job.RunEnv) (string, error) {
			return "ok", nil
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close(t.Context()) })

	sess, err := agent.NewSession(agent.WithCwd(t.TempDir()))
	require.NoError(t, err)
	eng, err := agent.NewEngine(
		llm.ModelConfig{Name: "fake", BaseURL: "http://127.0.0.1:9", APIKey: "x"},
		sess,
		agent.WithJobs(agenttool.Bind(mgr, sess.ID(), sess.Cwd())),
	)
	require.NoError(t, err)
	assert.True(t, eng.HasTool("agent_spawn"))

	eng.SetModel(llm.ModelConfig{Name: "fake2", BaseURL: "http://127.0.0.1:9", APIKey: "x"})
	assert.True(t, eng.HasTool("agent_spawn"))
}

func TestSetJobsTogglesAgentTools(t *testing.T) {
	mgr, err := job.New(job.Options{
		Root: t.TempDir(),
		Runner: job.RunnerFunc(func(_ context.Context, _ job.RunEnv) (string, error) {
			return "ok", nil
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close(t.Context()) })

	sess, err := agent.NewSession(agent.WithCwd(t.TempDir()))
	require.NoError(t, err)
	eng, err := agent.NewEngine(
		llm.ModelConfig{Name: "fake", BaseURL: "http://127.0.0.1:9", APIKey: "x"},
		sess,
	)
	require.NoError(t, err)
	assert.False(t, eng.HasTool("agent_spawn"))

	eng.SetJobs(agenttool.Bind(mgr, sess.ID(), sess.Cwd()))
	assert.True(t, eng.HasTool("agent_spawn"))

	eng.SetJobs(nil)
	assert.False(t, eng.HasTool("agent_spawn"))
	assert.True(t, eng.HasTool("bash")) // default tools still present
}

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
