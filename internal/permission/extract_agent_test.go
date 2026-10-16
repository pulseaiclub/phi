package permission_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/permission"
)

func TestExtractAgentAllow(t *testing.T) {
	root := t.TempDir()
	req, err := permission.ExtractAt("agent_spawn", json.RawMessage(`{"prompt":"x"}`), root)
	require.NoError(t, err)
	assert.Equal(t, permission.ActionAgentSpawn, req.Action)

	g, err := permission.NewGate(permission.DefaultPolicy(), root)
	require.NoError(t, err)
	dec, _ := g.Check(t.Context(), req)
	assert.Equal(t, permission.Allow, dec)
}

func TestExtractAgentControl(t *testing.T) {
	for _, tool := range []string{"agent_wait", "agent_cancel"} {
		t.Run(tool, func(t *testing.T) {
			req, err := permission.Extract(tool, json.RawMessage(`{"job_id":"j1"}`))
			require.NoError(t, err)
			assert.Equal(t, permission.ActionAgentControl, req.Action)
			assert.Empty(t, req.Paths)
		})
	}
}

func TestExtractAgentSpawnWorkdir(t *testing.T) {
	root := t.TempDir()
	req, err := permission.ExtractAt("agent_spawn", json.RawMessage(`{"prompt":"x","workdir":"sub"}`), root)
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(root, "sub")}, req.Paths)
}

func TestExtractAgentSpawnEffectiveWorkdir(t *testing.T) {
	processDir := t.TempDir()
	t.Chdir(processDir)
	root := filepath.Join(t.TempDir(), "session")
	for _, tc := range []struct {
		name string
		args string
		cwd  string
		want string
	}{
		{"omitted", `{"prompt":"x"}`, root, root},
		{"empty", `{"workdir":""}`, root, root},
		{"whitespace", `{"workdir":" \t "}`, root, root},
		{"parent", `{"workdir":".."}`, root, filepath.Dir(root)},
		{"process fallback", `{"prompt":"x"}`, "", processDir},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := permission.ExtractAt("agent_spawn", json.RawMessage(tc.args), tc.cwd)
			require.NoError(t, err)
			assert.Equal(t, []string{tc.want}, req.Paths)
		})
	}
}

func TestExtractAgentSpawnBadArgs(t *testing.T) {
	_, err := permission.Extract("agent_spawn", json.RawMessage(`{"workdir":`))
	require.Error(t, err)
}
