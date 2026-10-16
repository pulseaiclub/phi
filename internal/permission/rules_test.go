package permission

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRealPath(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	ws := filepath.Join(root, "ws")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.MkdirAll(ws, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(outside, "sub"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "existing"), []byte("original"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(ws, "dir")))
	require.NoError(t, os.Symlink("dir/existing", filepath.Join(ws, "file")))
	require.NoError(t, os.Symlink(filepath.Join(outside, "sub"), filepath.Join(ws, "sub")))
	require.NoError(t, os.Symlink("sub/../existing", filepath.Join(ws, "dotdot")))
	for _, tc := range []struct{ path, want string }{
		{"file", filepath.Join(outside, "existing")},
		{"dir/new/nested/file", filepath.Join(outside, "new/nested/file")},
		{"dotdot", filepath.Join(outside, "existing")},
		{"plain/new/file", filepath.Join(ws, "plain/new/file")},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got, err := realPath(filepath.Join(ws, tc.path))
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestRealPathRejectsDanglingLinks(t *testing.T) {
	ws := t.TempDir()
	require.NoError(t, os.Symlink("missing", filepath.Join(ws, "dangling")))
	require.NoError(t, os.Symlink("dangling", filepath.Join(ws, "nested")))
	gate, err := NewGate(DefaultPolicy(), ws)
	require.NoError(t, err)
	for _, path := range []string{"dangling", "dangling/child/file", "nested"} {
		_, err := realPath(filepath.Join(ws, path))
		require.Error(t, err)
		dec, reason := gate.Check(t.Context(), Request{Action: ActionWrite, Paths: []string{filepath.Join(ws, path)}})
		require.Equal(t, Deny, dec, reason)
	}
}

func TestGateRejectsNestedSymlinkEscape(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "file")
	require.NoError(t, os.WriteFile(target, []byte("original"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(ws, "dir")))
	require.NoError(t, os.Symlink("dir/file", filepath.Join(ws, "file")))
	require.NoError(t, os.Symlink("dir/missing", filepath.Join(ws, "missing")))
	require.NoError(t, os.Symlink(filepath.Join(outside, "new"), filepath.Join(ws, "newdir")))
	p := DefaultPolicy()
	p.WorkspaceOnlyReads = true
	g, err := NewGate(p, ws)
	require.NoError(t, err)
	for _, path := range []string{"file", "missing", "newdir/nested/file"} {
		for _, action := range []Action{ActionWrite, ActionEdit, ActionRead} {
			dec, reason := g.Check(t.Context(), Request{Action: action, Paths: []string{filepath.Join(ws, path)}})
			require.Equal(t, Deny, dec, "%s %s: %s", action, path, reason)
		}
	}
	content, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "original", string(content))
}

func TestGateRejectsUnresolvablePaths(t *testing.T) {
	ws := t.TempDir()
	require.NoError(t, os.Symlink("loop", filepath.Join(ws, "loop")))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "file"), nil, 0o600))
	require.NoError(t, os.Symlink("file/../safe", filepath.Join(ws, "notdir")))
	require.NoError(t, os.Symlink("file/.", filepath.Join(ws, "notdir-dot")))
	g, err := NewGate(DefaultPolicy(), ws)
	require.NoError(t, err)
	for _, path := range []string{"loop", "loop/child", "file/child", "notdir", "notdir-dot"} {
		_, err := realPath(filepath.Join(ws, path))
		require.Error(t, err)
		dec, reason := g.Check(t.Context(), Request{Action: ActionWrite, Paths: []string{filepath.Join(ws, path)}})
		require.Equal(t, Deny, dec, reason)
	}
	_, err = NewGate(DefaultPolicy(), filepath.Join(ws, "loop"))
	require.Error(t, err)
}

func TestGateRejectsDanglingSensitivePrefix(t *testing.T) {
	ws := t.TempDir()
	secret := filepath.Join(ws, "secret")
	require.NoError(t, os.Symlink("missing", secret))
	policy := DefaultPolicy()
	policy.SensitivePathDeny = []string{secret}
	gate, err := NewGate(policy, ws)
	require.NoError(t, err)
	dec, reason := gate.Check(t.Context(), Request{Action: ActionWrite, Paths: []string{filepath.Join(ws, "new")}})
	require.Equal(t, Deny, dec)
	require.Contains(t, reason, "cannot resolve sensitive path")
}

func TestGateSensitiveAliases(t *testing.T) {
	ws := t.TempDir()
	secret := filepath.Join(ws, "secret")
	require.NoError(t, os.Mkdir(secret, 0o700))
	alias := filepath.Join(ws, "alias")
	require.NoError(t, os.Symlink(secret, alias))
	p := DefaultPolicy()
	p.SensitivePathDeny = []string{alias}
	p.WorkspaceOnlyWrites = false
	g, err := NewGate(p, ws)
	require.NoError(t, err)
	for _, action := range []Action{ActionRead, ActionWrite, ActionEdit, ActionAgentSpawn} {
		dec, reason := g.Check(
			t.Context(),
			Request{Tool: "agent_spawn", Action: action, Paths: []string{filepath.Join(secret, "new")}},
		)
		require.Equal(t, Deny, dec, reason)
	}
}
