package agenttool_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/tools"
	"github.com/pulseaiclub/phi/internal/tools/agenttool"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

func newAgentTestRegistry(t *testing.T, runner job.RunnerFunc) (*job.Manager, tooldef.Registry) {
	t.Helper()
	mgr, err := job.New(job.Options{Root: t.TempDir(), Runner: runner})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close(t.Context()) })
	workdir := t.TempDir()
	reg := tooldef.NewRegistry(agenttool.AgentTools(
		agenttool.WithManager(mgr),
		agenttool.WithParentID(func() string { return "parent-1" }),
		agenttool.WithWorkDir(func() string { return workdir }),
	))
	return mgr, reg
}

func TestAgentToolsSpawnWaitForcesDepthAndParent(t *testing.T) {
	_, reg := newAgentTestRegistry(t, func(_ context.Context, env job.RunEnv) (string, error) {
		assert.Equal(t, "parent-1", env.Job.ParentID)
		assert.Equal(t, 0, env.Job.ParentDepth)
		return "summary-ok", nil
	})
	require.Contains(t, reg, "agent_spawn")
	require.Contains(t, reg, "agent_control")
	require.Len(t, reg, 2)
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

	waitArgs, _ := json.Marshal(map[string]any{"job_id": spawned.JobID, "action": "wait"})
	waitRes, err := reg["agent_control"].Run(t.Context(), waitArgs)
	require.NoError(t, err)
	assert.Contains(t, waitRes.Content, "summary-ok")
	assert.Contains(t, waitRes.Content, `"status": "completed"`)
	assert.Equal(t, "explore: d", waitRes.Detail)
}

func TestAgentToolsSpawnRoleWorker(t *testing.T) {
	_, reg := newAgentTestRegistry(t, func(_ context.Context, env job.RunEnv) (string, error) {
		assert.Equal(t, job.RoleWorker, env.Job.Role)
		return "done", nil
	})
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

	waitArgs, _ := json.Marshal(map[string]any{"job_id": spawned.JobID, "action": "wait"})
	waitRes, err := reg["agent_control"].Run(t.Context(), waitArgs)
	require.NoError(t, err)
	assert.Contains(t, waitRes.Content, `"status": "completed"`)
	assert.Equal(t, "worker: implement x", waitRes.Detail)
}

func TestAgentSpawnDetailAlwaysIncludesRole(t *testing.T) {
	_, reg := newAgentTestRegistry(t, func(_ context.Context, _ job.RunEnv) (string, error) {
		return "x", nil
	})
	detail := reg["agent_spawn"].DetailFromArgs
	require.NotNil(t, detail)

	assert.Equal(t, "explore: find auth", detail([]byte(`{"prompt":"p","description":"find auth"}`)))
	assert.Equal(t, "explore: do the thing", detail([]byte(`{"prompt":"do the thing"}`)))
	assert.Equal(t, "review: check diff", detail([]byte(`{"prompt":"p","description":"check diff","role":"review"}`)))
	// Multi-line prompts collapse: the label has to stay one TUI row.
	assert.Equal(t, "explore: find the auth config",
		detail([]byte(`{"prompt":"find the\n   auth config"}`)))
}

func TestAgentSpawnWorkdirMatchesPermissionRequest(t *testing.T) {
	t.Chdir(t.TempDir())
	sessionDir := filepath.Join(t.TempDir(), "session")
	for _, args := range []string{
		`{"prompt":"x"}`,
		`{"prompt":"x","workdir":""}`,
		`{"prompt":"x","workdir":" \t "}`,
		`{"prompt":"x","workdir":".."}`,
		`{"prompt":"x","workdir":"sub/../child"}`,
	} {
		t.Run(args, func(t *testing.T) {
			seen := make(chan string, 1)
			mgr, err := job.New(job.Options{
				Root: t.TempDir(),
				Runner: job.RunnerFunc(func(_ context.Context, env job.RunEnv) (string, error) {
					seen <- env.Job.WorkDir
					return "ok", nil
				}),
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = mgr.Close(context.WithoutCancel(t.Context())) })
			reg := tooldef.NewRegistry(agenttool.AgentTools(
				agenttool.WithManager(mgr),
				agenttool.WithWorkDir(func() string { return sessionDir }),
			))
			req, err := reg["agent_spawn"].Permission(json.RawMessage(args), sessionDir)
			require.NoError(t, err)
			_, err = reg["agent_spawn"].Run(t.Context(), json.RawMessage(args))
			require.NoError(t, err)
			select {
			case wd := <-seen:
				assert.True(t, filepath.IsAbs(wd))
				assert.Equal(t, []string{wd}, req.Paths)
			case <-time.After(2 * time.Second):
				require.Fail(t, "job did not start")
			}
		})
	}
}

func TestAgentToolsSpawnBadRole(t *testing.T) {
	_, reg := newAgentTestRegistry(t, func(_ context.Context, _ job.RunEnv) (string, error) {
		return "x", nil
	})
	raw, _ := json.Marshal(map[string]any{"prompt": "x", "role": "nope"})
	_, err := reg["agent_spawn"].Run(t.Context(), raw)
	require.Error(t, err)
}

func TestDefaultToolsExcludeAgent(t *testing.T) {
	for _, tool := range tools.DefaultTools(nil) {
		assert.NotContains(t, tool.Definition.Name, "agent_")
	}
}

func TestAgentToolsDualSpawnWaitKeepsTranscriptsInJobLog(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]bool{}
	mgr, reg := newAgentTestRegistry(t, func(_ context.Context, env job.RunEnv) (string, error) {
		mu.Lock()
		seen[env.Job.Description] = true
		mu.Unlock()
		env.Log(strings.Repeat("child-trace-line\n", 200))
		return "summary:" + env.Job.Description, nil
	})

	spawn := func(desc string) string {
		raw, _ := json.Marshal(map[string]any{"prompt": "p-" + desc, "description": desc})
		res, err := reg["agent_spawn"].Run(t.Context(), raw)
		require.NoError(t, err)
		var out struct {
			JobID string `json:"job_id"`
		}
		require.NoError(t, json.Unmarshal([]byte(res.Content), &out))
		return out.JobID
	}

	idA := spawn("A")
	idB := spawn("B")
	require.NotEqual(t, idA, idB)

	wait := func(id string) string {
		raw, _ := json.Marshal(map[string]any{"job_id": id, "action": "wait"})
		res, err := reg["agent_control"].Run(t.Context(), raw)
		require.NoError(t, err)
		var out struct {
			Status  string `json:"status"`
			Summary string `json:"summary"`
		}
		require.NoError(t, json.Unmarshal([]byte(res.Content), &out))
		assert.Equal(t, "completed", out.Status)
		return out.Summary
	}

	sumA := wait(idA)
	sumB := wait(idB)
	assert.Equal(t, "summary:A", sumA)
	assert.Equal(t, "summary:B", sumB)
	assert.True(t, seen["A"] && seen["B"])

	// Parent tool payload stays small despite huge child logs on disk.
	assert.Less(t, len(sumA)+len(sumB), 200)
	events, err := mgr.Log(t.Context(), idA, 0)
	require.NoError(t, err)
	require.NotEmpty(t, events)
	var sb strings.Builder
	for _, ev := range events {
		sb.WriteString(ev.Message)
	}
	joined := sb.String()
	assert.Contains(t, joined, "child-trace-line")
	assert.Greater(t, len(joined), 1000) // bulky transcript stayed in job log, not parent summary
}

func TestAgentToolsCancelRunningJob(t *testing.T) {
	started := make(chan struct{})
	_, reg := newAgentTestRegistry(t, func(ctx context.Context, _ job.RunEnv) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	})

	raw, _ := json.Marshal(map[string]any{"prompt": "hang"})
	spawnRes, err := reg["agent_spawn"].Run(t.Context(), raw)
	require.NoError(t, err)
	var spawned struct {
		JobID string `json:"job_id"`
	}
	require.NoError(t, json.Unmarshal([]byte(spawnRes.Content), &spawned))

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		require.Fail(t, "job did not start")
	}

	cancelRaw, _ := json.Marshal(map[string]any{"job_id": spawned.JobID, "action": "cancel"})
	_, err = reg["agent_control"].Run(t.Context(), cancelRaw)
	require.NoError(t, err)

	waitRaw, _ := json.Marshal(map[string]any{"job_id": spawned.JobID, "action": "wait"})
	waitRes, err := reg["agent_control"].Run(t.Context(), waitRaw)
	require.NoError(t, err)
	assert.Contains(t, waitRes.Content, `"status": "cancelled"`)
}

func TestChildToolsExcludeAgentAndWriteTools(t *testing.T) {
	for _, tool := range agent.ChildTools() {
		assert.NotEqual(t, "agent_spawn", tool.Definition.Name)
		assert.False(t, strings.HasPrefix(tool.Definition.Name, "agent_"))
		assert.NotEqual(t, "write", tool.Definition.Name)
		assert.NotEqual(t, "edit", tool.Definition.Name)
	}
}

func TestParseAgentResultSummary(t *testing.T) {
	out := `{
  "job_id": "job_1",
  "status": "completed",
  "error": "",
  "summary": "## Findings\n\n- ok\n"
}`
	r := agenttool.ParseAgentResult(out)
	require.True(t, r.OK)
	require.Equal(t, "job_1", r.JobID)
	require.Equal(t, "completed", r.Status)
	require.Equal(t, "## Findings\n\n- ok", r.RenderableSummary())
}

func TestParseAgentResultRunningNoSummary(t *testing.T) {
	r := agenttool.ParseAgentResult(`{"job_id":"j","status":"running"}`)
	require.True(t, r.OK)
	require.Empty(t, r.RenderableSummary())
}

func TestParseAgentResultRejectsPlain(t *testing.T) {
	assert.False(t, agenttool.ParseAgentResult("hello").OK, "expected reject")
}
