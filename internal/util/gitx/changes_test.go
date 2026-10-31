package gitx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseChanges(t *testing.T) {
	changes, err := parseChanges("MM both\x00R  new\x00old\x00 D deleted\x00?? space\nfile\x00UU conflict\x00")
	require.NoError(t, err)
	assert.Equal(t, []Change{
		{Path: "both", Status: 'M'},
		{Path: "conflict", Status: 'U'},
		{Path: "deleted", Status: 'D'},
		{Path: "new", OldPath: "old", Status: 'R'},
		{Path: "space\nfile", Status: '?'},
	}, changes)
	for _, text := range []string{"M", "?? missing-terminator", "R  new\x00", "R  new\x00\x00"} {
		_, err := parseChanges(text)
		require.Error(t, err)
	}
}

func TestParseChangesKeepsOneFileAcrossIndexAndWorktree(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   Change
	}{
		{"AM", Change{Path: "file", Status: 'A'}},
		{"MD", Change{Path: "file", Status: 'D'}},
		{"RM", Change{Path: "file", OldPath: "old", Status: 'R'}},
		{"RD", Change{Path: "file", OldPath: "old", Status: 'D'}},
		{" R", Change{Path: "file", OldPath: "old", Status: 'R'}},
		{"MR", Change{Path: "file", Status: 'M'}},
	} {
		t.Run(tc.status, func(t *testing.T) {
			record := tc.status + " file\x00"
			if strings.Contains(tc.status, "R") {
				record += "old\x00"
			}
			changes, err := parseChanges(record)
			require.NoError(t, err)
			assert.Equal(t, []Change{tc.want}, changes)
		})
	}
}

func TestChangesFromSubdirectory(t *testing.T) {
	dir := newRepo(t)
	path := filepath.Join(dir, "both")
	require.NoError(t, os.WriteFile(path, []byte("base\n"), 0o600))
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "base")
	require.NoError(t, os.WriteFile(path, []byte("index\n"), 0o600))
	gitRun(t, dir, "add", ".")
	require.NoError(t, os.WriteFile(path, []byte("worktree\n"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "new"), nil, 0o600))
	root, changes, err := ChangedFiles(t.Context(), filepath.Join(dir, "sub"))
	require.NoError(t, err)
	resolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	// git prints forward slashes even on windows, so compare separator-insensitively.
	assert.Equal(t, filepath.ToSlash(resolved), filepath.ToSlash(root))
	assert.Equal(t, []Change{
		{Path: "both", Status: 'M'},
		{Path: "sub/new", Status: '?'},
	}, changes)
}
