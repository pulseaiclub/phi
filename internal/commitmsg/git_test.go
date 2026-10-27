package commitmsg

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	return string(out)
}

func put(t *testing.T, dir, name, text string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600))
}

func repository(t *testing.T, committed bool) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "config", "user.email", "test@example.com")
	gitRun(t, dir, "config", "user.name", "Test")
	if committed {
		put(t, dir, "tracked", "original\n")
		put(t, dir, "deleted", "delete me\n")
		gitRun(t, dir, "add", ".")
		gitRun(t, dir, "commit", "-qm", "test: initial subject")
	}
	return dir
}

func TestCollectScope(t *testing.T) {
	t.Parallel()
	dir := repository(t, true)
	put(t, dir, "tracked", "staged version\n")
	put(t, dir, "addition", "staged addition\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "rm", "deleted")
	put(t, dir, "tracked", "working version\n")
	put(t, dir, "untracked", "untracked secret\n")
	before := gitRun(t, dir, "status", "--porcelain=v1")
	c := New(dir, nil)
	diff, history, truncated, err := c.collect(t.Context(), false)
	require.NoError(t, err)
	assert.False(t, truncated)
	assert.Contains(t, diff, "+staged version")
	assert.Contains(t, diff, "+staged addition")
	assert.Contains(t, diff, "deleted file mode")
	assert.NotContains(t, diff, "working version")
	assert.NotContains(t, diff, "untracked")
	assert.Equal(t, "test: initial subject\n", history)
	diff, _, _, err = c.collect(t.Context(), true)
	require.NoError(t, err)
	assert.Contains(t, diff, "+working version")
	assert.Contains(t, diff, "+staged addition")
	assert.Contains(t, diff, "deleted file mode")
	assert.NotContains(t, diff, "staged version")
	assert.NotContains(t, diff, "untracked")
	assert.Equal(t, before, gitRun(t, dir, "status", "--porcelain=v1"))
}

func TestCollectUnborn(t *testing.T) {
	t.Parallel()
	dir := repository(t, false)
	put(t, dir, "addition", "staged\n")
	gitRun(t, dir, "add", ".")
	put(t, dir, "addition", "working\n")
	put(t, dir, "untracked", "secret\n")
	for _, all := range []bool{false, true} {
		diff, history, _, err := New(dir, nil).collect(t.Context(), all)
		require.NoError(t, err)
		assert.Empty(t, history)
		assert.NotContains(t, diff, "untracked")
		if all {
			assert.Contains(t, diff, "+working")
		} else {
			assert.Contains(t, diff, "+staged")
		}
	}
}

func TestCollectBinaryAndBounds(t *testing.T) {
	t.Parallel()
	dir := repository(t, true)
	put(t, dir, "binary", "\x00\x01\x02")
	gitRun(t, dir, "add", ".")
	c := New(dir, nil)
	diff, _, _, err := c.collect(t.Context(), false)
	require.NoError(t, err)
	assert.Contains(t, diff, "Binary files")
	put(t, dir, "large", strings.Repeat("a long line of diff content\n", 4000))
	gitRun(t, dir, "add", ".")
	for i := range 7 {
		gitRun(t, dir, "commit", "--allow-empty", "-qm", strings.Repeat(string(rune('a'+i)), 600))
	}
	put(t, dir, "large", strings.Repeat("changed diff content\n", 4000))
	gitRun(t, dir, "add", ".")
	diff, history, truncated, err := c.collect(t.Context(), false)
	require.NoError(t, err)
	assert.True(t, truncated)
	assert.LessOrEqual(t, len(diff), diffLimit)
	assert.LessOrEqual(t, len(history), historyLimit)
	assert.NotContains(t, history, "initial subject")
}

func TestCollectTrackedDeletionAndRecentFive(t *testing.T) {
	t.Parallel()
	dir := repository(t, true)
	for _, subject := range []string{"first", "second", "third", "fourth", "fifth", "sixth"} {
		gitRun(t, dir, "commit", "--allow-empty", "-qm", subject)
	}
	require.NoError(t, os.Remove(filepath.Join(dir, "tracked")))
	c := New(dir, nil)
	diff, _, _, err := c.collect(t.Context(), false)
	require.NoError(t, err)
	assert.Empty(t, diff)
	diff, history, _, err := c.collect(t.Context(), true)
	require.NoError(t, err)
	assert.Contains(t, diff, "deleted file mode")
	assert.Equal(t, "sixth\nfifth\nfourth\nthird\nsecond\n", history)
}

func TestCollectFromSubdirectoryIgnoresRelativeConfig(t *testing.T) {
	t.Parallel()
	dir := repository(t, true)
	subdir := filepath.Join(dir, "sub")
	require.NoError(t, os.Mkdir(subdir, 0o700))
	gitRun(t, dir, "config", "diff.relative", "true")
	put(t, dir, "tracked", "root change\n")
	gitRun(t, dir, "add", "tracked")
	for _, all := range []bool{false, true} {
		diff, _, _, err := New(subdir, nil).collect(t.Context(), all)
		require.NoError(t, err)
		assert.Contains(t, diff, "+root change")
	}
}

func TestCollectExcludesSubmoduleDirt(t *testing.T) {
	t.Parallel()
	source := repository(t, true)
	dir := repository(t, true)
	gitRun(t, dir, "-c", "protocol.file.allow=always", "submodule", "add", source, "sub")
	gitRun(t, dir, "commit", "-qam", "test: add submodule")
	subdir := filepath.Join(dir, "sub")
	put(t, subdir, "tracked", "uncommitted submodule change\n")
	c := New(dir, nil)
	diff, _, _, err := c.collect(t.Context(), true)
	require.NoError(t, err)
	assert.Empty(t, diff)
	gitRun(
		t,
		subdir,
		"-c",
		"user.name=Test",
		"-c",
		"user.email=test@example.com",
		"commit",
		"-qam",
		"test: change submodule",
	)
	diff, _, _, err = c.collect(t.Context(), true)
	require.NoError(t, err)
	assert.Contains(t, diff, "Subproject commit")
	assert.NotContains(t, diff, "-dirty")
}

func TestCappedOutput(t *testing.T) {
	b := &cappedOutput{limit: 10}
	n, err := b.Write([]byte(strings.Repeat("x", 100)))
	require.NoError(t, err)
	assert.Equal(t, 100, n)
	assert.Len(t, b.data, 10)
	assert.True(t, b.truncated)
	n, err = b.Write([]byte("more"))
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.Len(t, b.data, 10)
}
