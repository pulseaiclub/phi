package permission

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMultiPathCheckRefreshesRequestAliases(t *testing.T) {
	for _, action := range []Action{ActionRead, ActionEdit} {
		t.Run(string(action), func(t *testing.T) {
			ws := t.TempDir()
			inside := filepath.Join(ws, "inside")
			require.NoError(t, os.Mkdir(inside, 0o700))
			alias := filepath.Join(ws, "alias")
			require.NoError(t, os.Symlink(inside, alias))
			policy := DefaultPolicy()
			policy.WorkspaceOnlyReads = true
			gate, err := NewGate(policy, ws)
			require.NoError(t, err)
			req := Request{Action: action, Paths: []string{filepath.Join(ws, "ordinary"), filepath.Join(alias, "file")}}
			dec, reason := gate.Check(t.Context(), req)
			require.Equal(t, Allow, dec, reason)
			require.NoError(t, os.Remove(alias))
			require.NoError(t, os.Symlink(t.TempDir(), alias))
			dec, reason = gate.Check(t.Context(), req)
			require.Equal(t, Deny, dec, reason)
		})
	}
}

func TestMultiPathCheckRefreshesSensitiveAliases(t *testing.T) {
	ws := t.TempDir()
	first := filepath.Join(ws, "first")
	second := filepath.Join(ws, "second")
	require.NoError(t, os.Mkdir(first, 0o700))
	require.NoError(t, os.Mkdir(second, 0o700))
	alias := filepath.Join(ws, "sensitive")
	require.NoError(t, os.Symlink(first, alias))
	policy := DefaultPolicy()
	policy.SensitivePathDeny = []string{alias}
	gate, err := NewGate(policy, ws)
	require.NoError(t, err)
	req := Request{Action: ActionEdit, Paths: []string{
		filepath.Join(ws, "ordinary"), filepath.Join(second, "new"),
	}}
	dec, reason := gate.Check(t.Context(), req)
	require.Equal(t, Allow, dec, reason)

	require.NoError(t, os.Remove(alias))
	require.NoError(t, os.Symlink(second, alias))
	dec, reason = gate.Check(t.Context(), req)
	require.Equal(t, Deny, dec, reason)
	require.Contains(t, reason, "sensitive path denied")
}
