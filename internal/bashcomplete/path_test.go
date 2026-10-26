package bashcomplete

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pathTestDir fills a directory with entries the two platform rules disagree
// on, and returns the names PathCommands should list plus one it should not.
// Windows decides by extension; unix decides by the mode bit.
func pathTestDir(t *testing.T) (dir string, want []string, skip string) {
	t.Helper()
	dir = t.TempDir()
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	for _, name := range []string{"git" + exe, "plain.txt", "noext" + exe} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), nil, 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "readme"+exe), nil, 0o644))
	if runtime.GOOS == "windows" {
		return dir, []string{"git.exe", "noext.exe", "readme.exe"}, "plain.txt"
	}
	return dir, []string{"git", "noext", "plain.txt"}, "readme"
}

func TestPathCommandsListsExecutablesAndBuiltins(t *testing.T) {
	dir, want, skip := pathTestDir(t)
	t.Setenv("PATH", dir)
	pathCacheMu.Lock()
	pathCache.names = nil
	pathCacheMu.Unlock()

	names := PathCommands()

	assert.Contains(t, names, "cd", "builtins are always completable")
	for _, name := range want {
		assert.Contains(t, names, name)
	}
	assert.NotContains(t, names, skip, "a file the platform would not run is not a command")
}

// The list is cached per PATH value: a keystroke must not rewalk PATH, and a
// changed PATH must not serve the stale list.
func TestPathCommandsIsCachedPerPathValue(t *testing.T) {
	dir, want, _ := pathTestDir(t)
	empty := t.TempDir()
	t.Setenv("PATH", dir)
	pathCacheMu.Lock()
	pathCache.names = nil
	pathCacheMu.Unlock()

	first := PathCommands()

	t.Setenv("PATH", empty)
	second := PathCommands()
	assert.NotContains(t, second, want[0], "a changed PATH is rescanned")

	t.Setenv("PATH", dir)
	assert.Equal(t, first, PathCommands(), "the same PATH reuses the scan")
}

func TestDedupeNames(t *testing.T) {
	assert.Equal(t, []string{"git", "ls"}, dedupeNames([]string{"git", "git", "ls", "git"}))
	if runtime.GOOS == "windows" {
		assert.Equal(t, []string{"Git.exe"}, dedupeNames([]string{"Git.exe", "git.exe"}))
	}
}
