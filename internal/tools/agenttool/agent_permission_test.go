package agenttool_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/tools/agenttool"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

// agentPermissionRegistry builds the agent tools with a fixed session workdir,
// which is what the permission declaration resolves relative paths against.
func agentPermissionRegistry(t *testing.T, workdir string) tooldef.Registry {
	t.Helper()
	mgr, err := job.New(job.Options{
		Root: t.TempDir(),
		Runner: job.RunnerFunc(func(context.Context, job.RunEnv) (string, error) {
			return "ok", nil
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close(context.WithoutCancel(t.Context())) })
	return tooldef.NewRegistry(agenttool.AgentTools(
		agenttool.WithManager(mgr),
		agenttool.WithWorkDir(func() string { return workdir }),
	))
}

func TestAgentSpawnPermissionWorkdir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "session")
	reg := agentPermissionRegistry(t, root)
	for _, tc := range []struct {
		name string
		args string
		want string
	}{
		{"omitted", `{"prompt":"x"}`, root},
		{"empty", `{"workdir":""}`, root},
		{"whitespace", `{"workdir":" \t "}`, root},
		{"relative", `{"workdir":"sub"}`, filepath.Join(root, "sub")},
		{"parent", `{"workdir":".."}`, filepath.Dir(root)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := reg["agent_spawn"].Permission(json.RawMessage(tc.args), root)
			require.NoError(t, err)
			assert.Equal(t, permission.KindDelegate, req.Kind)
			assert.Equal(t, []string{tc.want}, req.Paths)
		})
	}
}

func TestAgentSpawnPermissionFallsBackToProcessCwd(t *testing.T) {
	processDir := t.TempDir()
	t.Chdir(processDir)
	reg := agentPermissionRegistry(t, "")
	req, err := reg["agent_spawn"].Permission(json.RawMessage(`{"prompt":"x"}`), "")
	require.NoError(t, err)
	assert.Equal(t, []string{processDir}, req.Paths)
}

// Windows paths must survive JSON unescaping: the gate compares the workdir
// against the workspace, so a mangled backslash path would ask for a prompt
// that should not appear.
func TestAgentSpawnPermissionWindowsPath(t *testing.T) {
	reg := agentPermissionRegistry(t, "")
	args, err := json.Marshal(map[string]string{"prompt": "x", "workdir": `D:\a\phi\outside`})
	require.NoError(t, err)
	req, err := reg["agent_spawn"].Permission(args, "")
	require.NoError(t, err)
	require.Len(t, req.Paths, 1)
	assert.True(t, strings.HasSuffix(req.Paths[0], `D:\a\phi\outside`), req.Paths[0])
}

func TestAgentControlToolsCarryNoPath(t *testing.T) {
	reg := agentPermissionRegistry(t, t.TempDir())
	for _, tool := range []string{"agent_wait", "agent_cancel"} {
		t.Run(tool, func(t *testing.T) {
			req, err := reg[tool].Permission(json.RawMessage(`{"job_id":"j1"}`), "")
			require.NoError(t, err)
			assert.Equal(t, permission.KindControl, req.Kind)
			assert.Empty(t, req.Paths)
		})
	}
}

func TestAgentSpawnPermissionRejectsBadArgs(t *testing.T) {
	reg := agentPermissionRegistry(t, t.TempDir())
	_, err := reg.PermissionRequest("agent_spawn", json.RawMessage(`{"workdir":`), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent_spawn")
}

// An in-workspace spawn needs no prompt: the inherited cwd is gated exactly
// like an explicit workdir.
func TestAgentSpawnInWorkspacePermissionAllows(t *testing.T) {
	root := t.TempDir()
	reg := agentPermissionRegistry(t, root)
	gate, err := permission.NewGate(permission.DefaultPolicy(), root)
	require.NoError(t, err)
	req, err := reg.PermissionRequest("agent_spawn", json.RawMessage(`{"prompt":"x"}`), root)
	require.NoError(t, err)
	dec, reason := gate.Check(t.Context(), req)
	assert.Equal(t, permission.Allow, dec, reason)
}
