package agenttool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
)

func TestAgentControlSchema(t *testing.T) {
	_, reg := newAgentTestRegistry(t, func(context.Context, job.RunEnv) (string, error) {
		return "ok", nil
	})
	control := reg["agent_control"]
	assert.ElementsMatch(t, []string{"job_id", "action"}, control.Definition.Params.Required)
	action, ok := control.Definition.Params.Properties["action"].(llm.Object)
	require.True(t, ok)
	assert.Equal(t, []string{"wait", "cancel"}, action["enum"])
	assert.Equal(t, "j1", control.DetailFromArgs([]byte(`{"job_id":"j1","action":"wait"}`)))
}

func TestAgentControlRejectsInvalidArgs(t *testing.T) {
	_, reg := newAgentTestRegistry(t, func(context.Context, job.RunEnv) (string, error) {
		return "ok", nil
	})
	for _, tc := range []struct {
		args string
		want string
	}{
		{`{"job_id":`, "invalid"},
		{`{"job_id":"j1"}`, "action must be wait or cancel"},
		{`{"job_id":"j1","action":"stop"}`, "action must be wait or cancel"},
		{`{"action":"wait"}`, "job_id is required"},
		{`{"action":"cancel"}`, "job_id is required"},
	} {
		t.Run(tc.args, func(t *testing.T) {
			_, err := reg["agent_control"].Run(t.Context(), json.RawMessage(tc.args))
			require.ErrorIs(t, err, job.ErrInvalid)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
	for _, action := range []string{"wait", "cancel"} {
		raw, err := json.Marshal(map[string]string{"job_id": "missing", "action": action})
		require.NoError(t, err)
		_, err = reg["agent_control"].Run(t.Context(), raw)
		require.Error(t, err, action)
	}
}

func TestAgentControlWaitTimeoutKeepsJobRunning(t *testing.T) {
	mgr, reg := newAgentTestRegistry(t, func(ctx context.Context, _ job.RunEnv) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	spawn, err := reg["agent_spawn"].Run(t.Context(), []byte(`{"prompt":"wait for cancellation"}`))
	require.NoError(t, err)
	var spawned struct {
		JobID string `json:"job_id"`
	}
	require.NoError(t, json.Unmarshal([]byte(spawn.Content), &spawned))
	raw, err := json.Marshal(map[string]any{"job_id": spawned.JobID, "action": "wait", "timeout_sec": 1})
	require.NoError(t, err)
	result, err := reg["agent_control"].Run(t.Context(), raw)
	require.NoError(t, err)
	assert.Contains(t, result.Content, `"status": "running"`)
	info, err := mgr.Get(t.Context(), spawned.JobID)
	require.NoError(t, err)
	assert.Equal(t, job.StatusRunning, info.Status)

	raw, err = json.Marshal(map[string]string{"job_id": spawned.JobID, "action": "cancel"})
	require.NoError(t, err)
	result, err = reg["agent_control"].Run(t.Context(), raw)
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true}`, result.Content)
	assert.Equal(t, "stopped", result.Detail)
	info, err = mgr.Get(t.Context(), spawned.JobID)
	require.NoError(t, err)
	assert.Equal(t, job.StatusCancelled, info.Status)
}
