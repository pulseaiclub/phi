package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	binName := executableName("fd")
	require.NoError(t, os.WriteFile(filepath.Join(p.Global().BinDir(), binName), []byte("x"), 0o755))

	assert.False(t, shouldBootstrap(p, "fd"))
	assert.True(t, shouldBootstrap(p, "rg"))
}

func TestShouldBootstrapWhenOnPATH(t *testing.T) {
	p, pathDir := testProject(t)
	binName := executableName("rg")
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
			<-result //nolint:errcheck // goroutine error is irrelevant on this fail path
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
