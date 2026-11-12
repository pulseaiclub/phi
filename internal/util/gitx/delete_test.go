package gitx

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeleteSquashMergedBranch(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "checkout", "-b", "feature")
	writeFile(t, dir, "feature.txt", "feature\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "feature")
	gitRun(t, dir, "checkout", "main")
	gitRun(t, dir, "merge", "--squash", "feature")
	gitRun(t, dir, "commit", "-m", "squash feature")

	require.NoError(t, Delete(t.Context(), dir, "feature"))
	branches, err := Branches(t.Context(), dir)
	require.NoError(t, err)
	assert.NotContains(t, branchNames(branches), "feature")
}

func TestDeleteKeepsGitProtections(t *testing.T) {
	dir := newRepo(t)
	worktree := filepath.Join(t.TempDir(), "other")
	gitRun(t, dir, "worktree", "add", "-b", "occupied", worktree)
	for _, name := range []string{"main", "occupied", "missing", "-D"} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, Delete(t.Context(), dir, name))
		})
	}
	branches, err := Branches(t.Context(), dir)
	require.NoError(t, err)
	assert.Contains(t, branchNames(branches), "main")
	assert.Contains(t, branchNames(branches), "occupied")
}
