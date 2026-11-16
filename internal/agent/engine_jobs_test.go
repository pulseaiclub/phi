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

func TestNewEngineRegistersJobs(t *testing.T) {
	mgr, err := job.New(t.TempDir(), job.RunnerFunc(func(_ context.Context, _ job.RunEnv) (string, error) {
		return "ok", nil
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close(t.Context()) })

	sess, err := agent.NewSession(agent.WithCwd(t.TempDir()))
	require.NoError(t, err)
	eng, err := agent.NewEngine(
		llm.ModelConfig{Name: "fake", BaseURL: "http://127.0.0.1:9", APIKey: "x"},
		sess,
		agent.WithJobs(mgr),
	)
	require.NoError(t, err)
	assert.True(t, eng.HasTool("agent_spawn"))

	reconfigured, err := agent.NewEngine(
		llm.ModelConfig{Name: "fake2", BaseURL: "http://127.0.0.1:9", APIKey: "x"},
		sess,
		agent.WithJobs(mgr),
	)
	require.NoError(t, err)
	assert.True(t, reconfigured.HasTool("agent_spawn"))
}

func TestSetJobsTogglesAgentTools(t *testing.T) {
	mgr, err := job.New(t.TempDir(), job.RunnerFunc(func(_ context.Context, _ job.RunEnv) (string, error) {
		return "ok", nil
	}))
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

	withJobs, err := agent.NewEngine(
		llm.ModelConfig{Name: "fake", BaseURL: "http://127.0.0.1:9", APIKey: "x"},
		sess,
		agent.WithJobs(mgr),
	)
	require.NoError(t, err)
	assert.True(t, withJobs.HasTool("agent_spawn"))

	withoutJobs, err := agent.NewEngine(
		llm.ModelConfig{Name: "fake", BaseURL: "http://127.0.0.1:9", APIKey: "x"},
		sess,
	)
	require.NoError(t, err)
	assert.False(t, withoutJobs.HasTool("agent_spawn"))
	assert.True(t, withoutJobs.HasTool("bash")) // default tools still present
}

func TestChildToolsHaveNoAgent(t *testing.T) {
	for _, tool := range agent.ChildTools() {
		assert.NotContains(t, tool.Definition.Name, "agent_")
	}
}

func TestChildToolsAreReadonly(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range agent.ChildTools() {
		names[tool.Definition.Name] = true
	}
	assert.True(t, names["read"])
	assert.True(t, names["grep"])
	assert.True(t, names["bash"])
	assert.False(t, names["write"])
	assert.False(t, names["edit"])
}
