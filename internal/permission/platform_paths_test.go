package permission

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSensitiveCaseAlias(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("conservative case matching applies to macOS and Windows")
	}
	ws := t.TempDir()
	secret := filepath.Join(ws, "secret")
	require.NoError(t, os.Mkdir(secret, 0o700))
	alias := filepath.Join(ws, "alias")
	require.NoError(t, os.Symlink(filepath.Join(ws, "SECRET"), alias))
	p := DefaultPolicy()
	p.SensitivePathDeny = []string{secret}
	p.WorkspaceOnlyWrites = false
	g, err := NewGate(p, ws)
	require.NoError(t, err)
	for _, kind := range []Kind{KindRead, KindWrite} {
		dec, reason := g.Check(t.Context(), Request{Kind: kind, Paths: []string{filepath.Join(alias, "new")}})
		require.Equal(t, Deny, dec, reason)
	}
}

func TestWindowsJunctionDenied(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows junction")
	}
	ws := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(ws, "junction")
	output, err := exec.CommandContext(t.Context(), "cmd", "/c", "mklink", "/J", link, outside).CombinedOutput()
	require.NoError(t, err, string(output))
	p := DefaultPolicy()
	p.WorkspaceOnlyReads = true
	g, err := NewGate(p, ws)
	require.NoError(t, err)
	for _, path := range []string{link, filepath.Join(link, "file")} {
		for _, kind := range []Kind{KindRead, KindWrite, KindDelegate} {
			dec, reason := g.Check(t.Context(), Request{Kind: kind, Paths: []string{path}})
			require.Equal(t, Deny, dec, reason)
		}
	}
	_, err = NewGate(p, link)
	require.Error(t, err)
}
