package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/project"
)

// testProject discovers a project under a temp HOME so tests never touch the
// real ~/.phi, and returns a project plus a PATH dir for binary stubs.
func testProject(t *testing.T) (*project.Project, string) {
	t.Helper()
	home := t.TempDir()
	pathDir := t.TempDir()
	// os.UserHomeDir uses HOME on Unix and USERPROFILE on Windows.
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", pathDir)

	p, err := project.Discover("")
	require.NoError(t, err)
	return p, pathDir
}

func TestShouldBootstrapWhenMissing(t *testing.T) {
	p, _ := testProject(t)
	// Empty bin dir and empty PATH dir → must download.
	assert.True(t, shouldBootstrap(p, "fd"))
	assert.True(t, shouldBootstrap(p, "rg"))
}

func TestShouldBootstrapWhenInBinDir(t *testing.T) {
	p, _ := testProject(t)
	binName := "fd"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	require.NoError(t, os.WriteFile(filepath.Join(p.Global().BinDir(), binName), []byte("x"), 0o755))

	assert.False(t, shouldBootstrap(p, "fd"))
	assert.True(t, shouldBootstrap(p, "rg"))
}

func TestShouldBootstrapWhenOnPATH(t *testing.T) {
	p, pathDir := testProject(t)
	binName := "rg"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	require.NoError(t, os.WriteFile(filepath.Join(pathDir, binName), []byte("x"), 0o755))

	assert.False(t, shouldBootstrap(p, "rg"))
	assert.True(t, shouldBootstrap(p, "fd"))
}

func TestEnsureSearchToolsAttemptsAllDownloads(t *testing.T) {
	p, _ := testProject(t)
	started := make(chan string, 2)
	release := make(chan struct{})
	download := func(_ context.Context, tool string) (string, error) {
		started <- tool
		<-release
		return "", errors.New("download failed")
	}

	result := make(chan error, 1)
	go func() {
		result <- ensureSearchTools(t.Context(), p, download)
	}()

	downloaded := make([]string, 0, 2)
	for len(downloaded) < 2 {
		select {
		case tool := <-started:
			downloaded = append(downloaded, tool)
		case <-time.After(time.Second):
			close(release)
			<-result
			require.Fail(t, "downloads did not start concurrently")
		}
	}
	close(release)
	err := <-result
	require.Error(t, err)
	assert.ElementsMatch(t, []string{"fd", "rg"}, downloaded)
	assert.ErrorContains(t, err, "fd: download failed")
	assert.ErrorContains(t, err, "rg: download failed")
}

func TestHeadlessGateDefaultsToStrict(t *testing.T) {
	// Empty mode + Ask-default bash must fold to Deny (Ask≡Deny).
	policy := permission.DefaultPolicy()
	policy.Mode = "" // unset → headless-strict
	gate, err := HeadlessGate(policy)
	require.NoError(t, err)

	dec, reason := gate.Check(t.Context(), permission.Request{
		Kind:    permission.KindExec,
		Command: "pip install numpy",
	})
	assert.Equal(t, permission.Deny, dec)
	assert.Contains(t, reason, "headless-strict")

	// Allowlisted simple command still allowed.
	dec, _ = gate.Check(t.Context(), permission.Request{
		Kind:    permission.KindExec,
		Command: "pwd",
	})
	assert.Equal(t, permission.Allow, dec)
}

func TestHeadlessGateDangerouslyAllowAll(t *testing.T) {
	policy := permission.DefaultPolicy()
	policy.DangerouslyAllowAll = true
	gate, err := HeadlessGate(policy)
	require.NoError(t, err)

	dec, _ := gate.Check(t.Context(), permission.Request{
		Kind:    permission.KindExec,
		Command: "rm -rf /",
	})
	assert.Equal(t, permission.Allow, dec)
}

func TestLoadRunBootstrapYolo(t *testing.T) {
	p, pathDir := testProject(t)
	// Stub search tools on PATH so EnsureSearchTools does not hit the network.
	for _, name := range []string{"fd", "rg"} {
		binName := name
		if runtime.GOOS == "windows" {
			binName += ".exe"
		}
		require.NoError(t, os.WriteFile(filepath.Join(pathDir, binName), []byte("x"), 0o755))
	}
	cfgPath := p.Global().ConfigFile()
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o755))
	require.NoError(t, os.WriteFile(cfgPath, []byte(`models:
  - name: m
    api_key: k
permissions:
  mode: headless-strict
`), 0o644))

	bs, err := loadRunBootstrap(t.Context(), "", true)
	require.NoError(t, err)
	require.NotNil(t, bs.Gate)

	dec, _ := bs.Gate.Check(t.Context(), permission.Request{
		Kind:    permission.KindExec,
		Command: "pip install numpy",
	})
	assert.Equal(t, permission.Allow, dec)
}
