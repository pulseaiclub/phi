package pathutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShortPath(t *testing.T) {
	for _, tc := range []struct {
		name, home, path, want string
	}{
		{"home", "/users/phi", "/users/phi", "~"},
		{"child", "/users/phi", "/users/phi/repo", "~/repo"},
		{"sibling prefix", "/users/phi", "/users/phi-backup/repo", "/users/phi-backup/repo"},
		{"unrelated", "/users/phi", "/work/repo", "/work/repo"},
		{"deep child", "/users/phi", "/users/phi/a/b/c/d/e", "~/a/../d/e"},
		{"empty", "/users/phi", "", ""},
		{"missing home", "", "/work/repo", "/work/repo"},
		{"trailing separator", "/users/phi/", "/users/phi/repo", "~/repo"},
		{"root home", "/", "/repo", "~/repo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := filepath.FromSlash(tc.home)
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			assert.Equal(t, filepath.FromSlash(tc.want), ShortPath(filepath.FromSlash(tc.path)))
		})
	}
}

func TestGitBranchOutsideRepo(t *testing.T) {
	assert.Empty(t, GitBranch(t.Context(), t.TempDir()), "non-git dir has no branch")
}

func TestPathWithBranchOutsideRepo(t *testing.T) {
	plain := t.TempDir()
	assert.Equal(t, ShortPath(plain), PathWithBranch(plain), "no branch suffix outside git")
}

func TestResolveGitDir(t *testing.T) {
	dir := t.TempDir()
	assert.Equal(t, filepath.Join(dir, ".git"), resolveGitDir(dir))

	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: ../modules/sub\n"), 0o644))
	assert.Equal(t, filepath.Join(filepath.Dir(dir), "modules", "sub"), resolveGitDir(dir))

	abs := filepath.Join(t.TempDir(), "modules", "sub")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+abs+"\n"), 0o644))
	assert.Equal(t, abs, resolveGitDir(dir))
}

func TestBranchState(t *testing.T) {
	dir := t.TempDir()
	assert.Equal(t, "missing", branchState(dir))

	gitDir := filepath.Join(dir, ".git")
	require.NoError(t, os.Mkdir(gitDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	assert.Equal(t, "ref: refs/heads/main", branchState(dir))

	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("abc123\n"), 0o644))
	assert.Equal(t, "abc123", branchState(dir))
}

func TestWatchBranchFiresOnChange(t *testing.T) {
	dir := t.TempDir()
	// PathWithBranch shells out to real git, so the fixture must be a real repo.
	require.NoError(t, exec.CommandContext(t.Context(), "git", "-C", dir, "init", "-q").Run())
	gitDir := filepath.Join(dir, ".git")

	labels := make(chan string, 1)
	WatchBranch(dir, func(label string) {
		labels <- label
	})

	// Wait for the first poll to establish baseline, then change HEAD.
	time.Sleep(1100 * time.Millisecond)
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/feature\n"), 0o644))

	select {
	case label := <-labels:
		assert.Contains(t, label, "feature")
	case <-time.After(5 * time.Second):
		require.Fail(t, "timeout waiting for branch label update")
	}
}
