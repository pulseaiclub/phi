package diffreview

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/util/gitx"
)

func TestLoadChangeLiteralPaths(t *testing.T) {
	// The last case is the one that makes git C-quote the header, which is what
	// the pane rewrite has to undo. A non-ascii name reaches that code on every
	// platform; a newline or quote in a name cannot be created on windows at all.
	for _, path := range []string{"HEAD", "-option", "[a].txt", "space file", "文件.txt"} {
		t.Run(path, func(t *testing.T) {
			dir := newGitRepo(t)
			write := func(name, body string) {
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600))
			}
			write(path, "index-version\n")
			write("other", "unrelated\n")
			runGit(t, dir, "git", "add", ".")
			text, err := LoadChange(t.Context(), dir, gitx.Change{Path: path})
			require.NoError(t, err, "initial repository has no HEAD")
			assert.Contains(t, text, "+index-version")
			assert.NotContains(t, text, "unrelated")
			write(path, "worktree-version\n")
			text, err = LoadChange(t.Context(), dir, gitx.Change{Path: path})
			require.NoError(t, err)
			assert.Contains(t, text, "+worktree-version")
			runGit(t, dir, "git", "commit", "-m", "base")
			text, err = LoadChange(t.Context(), dir, gitx.Change{Path: path})
			require.NoError(t, err)
			assert.Contains(t, text, "-index-version")
			assert.Contains(t, text, "+worktree-version")
			require.NoError(t, os.Remove(filepath.Join(dir, path)))
			text, err = LoadChange(t.Context(), dir, gitx.Change{Path: path})
			require.NoError(t, err)
			assert.Contains(t, text, "deleted file mode")
		})
	}
}

func TestLoadChangeUntrackedAndConflict(t *testing.T) {
	dir := newGitRepo(t)
	for _, tc := range []struct{ path, content string }{
		{"plain", "new content\n"},
		{"empty", ""},
		{"binary", "\x00\x01"},
		{"conflict", "<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>> branch\n"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			require.NoError(t, os.WriteFile(filepath.Join(dir, tc.path), []byte(tc.content), 0o600))
			status := byte('?')
			if tc.path == "conflict" {
				status = 'U'
			}
			text, err := LoadChange(t.Context(), dir, gitx.Change{Status: status, Path: tc.path})
			require.NoError(t, err)
			doc, err := Parse(text)
			require.NoError(t, err)
			require.Len(t, doc.Files, 1)
			assert.NotEmpty(t, doc.Rows())
			if tc.path == "conflict" {
				assert.Contains(t, text, "+<<<<<<< HEAD")
			}
		})
	}
	_, changes, err := gitx.ChangedFiles(t.Context(), dir)
	require.NoError(t, err)
	for _, change := range changes {
		assert.Equal(t, byte('?'), change.Status, "viewing must not stage files")
	}
	text, err := LoadChange(t.Context(), dir, gitx.Change{Status: 'U', Path: "deleted"})
	require.NoError(t, err)
	assert.Contains(t, text, "Unresolved conflict")
}

func TestLoadChangeRenameAndMode(t *testing.T) {
	dir := newGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "old"), []byte("content\n"), 0o600))
	runGit(t, dir, "git", "add", ".")
	runGit(t, dir, "git", "commit", "-m", "base")
	runGit(t, dir, "git", "mv", "old", "new")
	root, changes, err := gitx.ChangedFiles(t.Context(), dir)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	text, err := LoadChange(t.Context(), root, changes[0])
	require.NoError(t, err)
	assert.Contains(t, text, "rename from old")
	assert.Contains(t, text, "rename to new")
	runGit(t, dir, "git", "commit", "-m", "rename")
	// Record the mode in the index: os.Chmod needs an executable bit, which NTFS
	// does not have, and core.fileMode=false (the fixture default) would ignore
	// the stat cache anyway.
	runGit(t, dir, "git", "update-index", "--chmod=+x", "new")
	text, err = LoadChange(t.Context(), root, gitx.Change{Path: "new"})
	require.NoError(t, err)
	assert.Contains(t, text, "new mode 100755")
}
