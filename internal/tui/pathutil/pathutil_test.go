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

func TestBranchState(t *testing.T) {
	dir := t.TempDir()
	assert.Equal(t, "missing", branchState(dir))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	assert.Equal(t, "ref: refs/heads/main", branchState(dir))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "HEAD"), []byte("abc123\n"), 0o644))
	assert.Equal(t, "abc123", branchState(dir))
}

func TestWatchBranchFiresFromSubdirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, exec.CommandContext(t.Context(), "git", "-C", dir, "init", "-q", "-b", "main").Run())
	nested := filepath.Join(dir, "docs", "examples")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	require.Equal(t, ShortPath(nested)+" (main)", PathWithBranch(nested))

	labels := make(chan string, 1)
	stop := make(chan struct{})
	defer close(stop)
	watcher := newBranchWatch(nested, 10*time.Millisecond)
	go watcher.run(stop, func(label string) {
		labels <- label
	})

	require.NoError(
		t,
		exec.CommandContext(t.Context(), "git", "-C", dir, "symbolic-ref", "HEAD", "refs/heads/feature").Run(),
	)

	select {
	case label := <-labels:
		assert.Equal(t, ShortPath(nested)+" (feature)", label)
	case <-time.After(2 * time.Second):
		require.Fail(t, "timeout waiting for branch label update")
	}
}
