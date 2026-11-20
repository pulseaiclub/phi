package agent_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
)

func TestEngineConfigRegistersJobs(t *testing.T) {
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
	eng, err := (agent.EngineConfig{Jobs: mgr}).NewEngine(
		llm.ModelConfig{Name: "fake", BaseURL: "http://127.0.0.1:9", APIKey: "x"},
		sess,
	)
	require.NoError(t, err)
	assert.True(t, eng.HasTool("agent_spawn"))
}

func TestEngineConfigCoreToolsDoesNotMutateInputs(t *testing.T) {
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
	base := agent.SpecForRole(job.RoleExplore).Tools
	cfg := agent.EngineConfig{Tools: base, Jobs: mgr}
	got := cfg.CoreTools(sess)
	assert.Len(t, base, len(agent.SpecForRole(job.RoleExplore).Tools))
	assert.Greater(t, len(got), len(base))
	assert.NotNil(t, cfg.Jobs)
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
