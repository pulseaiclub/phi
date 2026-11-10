package agenttool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/tools/agenttool"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

func TestSpawnContextModes(t *testing.T) {
	for _, tc := range []struct {
		name string
		role string
		mode string
		want job.ContextMode
	}{
		{"default role", "", "", job.ContextModeHandoff},
		{"explore", "explore", "", job.ContextModeHandoff},
		{"worker", "worker", "", job.ContextModeHandoff},
		{"review", "review", "", job.ContextModeClean},
		{"clean explore", "explore", "clean", job.ContextModeClean},
		{"clean worker", "worker", "clean", job.ContextModeClean},
		{"handoff review", "review", "handoff", job.ContextModeHandoff},
		{"normalized", " REVIEW ", " HANDOFF ", job.ContextModeHandoff},
		{"blank default", "worker", "  ", job.ContextModeHandoff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := make(chan job.ParentContext, 1)
			mgr, err := job.New(job.Options{
				Root: t.TempDir(),
				Runner: job.RunnerFunc(func(_ context.Context, env job.RunEnv) (string, error) {
					seen <- env.ParentContext
					return "ok", nil
				}),
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = mgr.Close(context.WithoutCancel(t.Context())) })
			calls := 0
			reg := tooldef.NewRegistry(agenttool.AgentTools(
				agenttool.WithManager(mgr),
				agenttool.WithParentContext(func() job.ParentContext {
					calls++
					return job.ParentContext{Mode: job.ContextModeHandoff, Text: "trusted background"}
				}),
			))
			args, err := json.Marshal(map[string]string{
				"prompt": "work", "role": tc.role, "context_mode": tc.mode,
			})
			require.NoError(t, err)
			result, err := reg["agent_spawn"].Run(t.Context(), args)
			require.NoError(t, err)
			var info struct {
				JobID string `json:"job_id"`
			}
			require.NoError(t, json.Unmarshal([]byte(result.Content), &info))
			_, err = mgr.Wait(t.Context(), info.JobID)
			require.NoError(t, err)
			got := <-seen
			assert.Equal(t, tc.want, got.Mode)
			if tc.want == job.ContextModeClean {
				assert.Zero(t, calls)
				assert.Empty(t, got.Text)
			} else {
				assert.Equal(t, 1, calls)
				assert.Equal(t, "trusted background", got.Text)
			}
		})
	}
}

func TestSpawnInvalidContextModeDoesNotCapture(t *testing.T) {
	mgr, _ := newAgentTestRegistry(t, func(_ context.Context, _ job.RunEnv) (string, error) {
		assert.Fail(t, "invalid mode must not start a job")
		return "", nil
	})
	calls := 0
	reg := tooldef.NewRegistry(agenttool.AgentTools(
		agenttool.WithManager(mgr),
		agenttool.WithParentContext(func() job.ParentContext {
			calls++
			return job.ParentContext{}
		}),
	))
	_, err := reg["agent_spawn"].Run(t.Context(), json.RawMessage(`{"prompt":"work","context_mode":"fork"}`))
	require.ErrorIs(t, err, job.ErrInvalid)
	assert.Zero(t, calls)
}
