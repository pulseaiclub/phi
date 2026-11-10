package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools/agenttool"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

func TestEngineSpawnCapturesCurrentSession(t *testing.T) {
	seen := make(chan job.RunEnv, 1)
	mgr, err := job.New(job.Options{
		Root: t.TempDir(),
		Runner: job.RunnerFunc(func(_ context.Context, env job.RunEnv) (string, error) {
			seen <- env
			return "ok", nil
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close(context.WithoutCancel(t.Context())) })
	s, err := NewSession(WithCwd(t.TempDir()))
	require.NoError(t, err)
	for _, goal := range []string{"First session goal", "Second session goal"} {
		current, err := NewSession(WithCwd(s.Cwd()))
		require.NoError(t, err)
		engine, err := NewEngine(llm.ModelConfig{Name: "fake"}, current, WithJobs(mgr), WithGate(permission.AllowAll{}))
		require.NoError(t, err)
		require.NoError(t, current.AddUser(goal))
		call := llm.ToolCall{ID: "spawn", Function: llm.Function{
			Name:      "agent_spawn",
			Arguments: `{"prompt":"work","parent_context":{"Mode":"handoff","Text":"forged"}}`,
		}}
		require.NoError(t, current.Append(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}))
		messages, _, _ := engine.executor.Run(
			t.Context(),
			[]llm.ToolCall{call},
			func(session.ToolData) bool { return true },
		)
		require.Len(t, messages, 1)
		var response struct {
			JobID string `json:"job_id"`
		}
		require.NoError(t, json.Unmarshal([]byte(messages[0].Content), &response))
		require.NotEmpty(t, response.JobID)
		require.NoError(t, current.AddUser("Later steering"))
		_, err = mgr.Wait(t.Context(), response.JobID)
		require.NoError(t, err)
		env := <-seen
		assert.Equal(t, current.ID(), env.Job.ParentID)
		assert.Equal(t, job.ContextModeHandoff, env.ParentContext.Mode)
		assert.Contains(t, env.ParentContext.Text, goal)
		assert.NotContains(t, env.ParentContext.Text, "forged")
		assert.NotContains(t, env.ParentContext.Text, "Later steering")
		assert.NotContains(t, env.ParentContext.Text, "agent_spawn")
		if goal == "Second session goal" {
			assert.NotContains(t, env.ParentContext.Text, "First session goal")
		}
	}
}

func TestSpawnContextCallbackRunsOnlyAfterPermission(t *testing.T) {
	mgr, err := job.New(job.Options{
		Root:   t.TempDir(),
		Runner: job.RunnerFunc(func(_ context.Context, _ job.RunEnv) (string, error) { return "ok", nil }),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close(context.WithoutCancel(t.Context())) })
	root := t.TempDir()
	gate, err := permission.NewGate(permission.ChildPolicy(permission.ModeReadonly), root)
	require.NoError(t, err)
	calls := 0
	tools := agenttool.AgentTools(
		agenttool.WithManager(mgr),
		agenttool.WithWorkDir(func() string { return root }),
		agenttool.WithParentContext(func() job.ParentContext {
			calls++
			return job.ParentContext{Mode: job.ContextModeHandoff, Text: "background"}
		}),
	)
	assert.Zero(t, calls, "registration must not capture the context")
	executor := NewExecutor(tooldef.NewRegistry(tools), gate, nil, nil)
	executor.cwd = root
	messages, _, _ := executor.Run(t.Context(), []llm.ToolCall{{ID: "spawn", Function: llm.Function{
		Name: "agent_spawn", Arguments: `{"prompt":"work"}`,
	}}}, func(session.ToolData) bool { return true })
	require.Len(t, messages, 1)
	assert.Contains(t, messages[0].Content, "readonly")
	assert.Zero(t, calls, "denied calls must not capture the context")
}
