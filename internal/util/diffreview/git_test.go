package diffreview

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	runGit(t, dir, "git", "init", "--template=")
	runGit(t, dir, "git", "config", "user.email", "t@t")
	runGit(t, dir, "git", "config", "user.name", "t")
	// Windows CI often sets autocrlf=true, which makes git print an LF/CRLF
	// warning on stderr; LoadGit merges stderr into the diff text.
	runGit(t, dir, "git", "config", "core.autocrlf", "false")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(
		os.Environ(),
		"GIT_AUTHOR_NAME=t",
		"GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t",
		"GIT_COMMITTER_EMAIL=t@t",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "Operation not permitted") {
			t.Skipf("sandbox blocked git: %s", out)
		}
		require.NoError(t, err, string(out))
	}
}

func TestLoadGitWorkingTree(t *testing.T) {
	dir := newGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("old\n"), 0o644))
	runGit(t, dir, "git", "add", "a.txt")
	runGit(t, dir, "git", "commit", "-m", "init")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("new\n"), 0o644))

	text, err := LoadGit(t.Context(), dir, nil)
	require.NoError(t, err)
	assert.Contains(t, text, "diff --git")
	assert.Contains(t, text, "-old")
	assert.Contains(t, text, "+new")

	_, err = Parse(text)
	require.NoError(t, err)
}

func TestLoadGitMissingRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	_, err := LoadGit(t.Context(), t.TempDir(), nil)
	require.Error(t, err)
	// git answers with a warning plus the whole `git diff` usage dump; the status
	// bar gets the warning and the next step, not 4 KB of options.
	assert.Contains(t, err.Error(), "Not a git repository")
	assert.Contains(t, err.Error(), "this directory is not a git repository")
	assert.NotContains(t, err.Error(), "usage:")
	assert.NotContains(t, err.Error(), "\n")
	assert.Less(t, len(err.Error()), 300)
}

func TestLoadGitUnknownRevision(t *testing.T) {
	dir := newGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x\n"), 0o644))
	runGit(t, dir, "git", "add", "a.txt")
	runGit(t, dir, "git", "commit", "-m", "init")

	_, err := LoadGit(t.Context(), dir, []string{"deadbeef"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deadbeef")
	assert.Contains(t, err.Error(), "fetch first")
	assert.NotContains(t, err.Error(), "\n")
}

func TestNormalizeSpec(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt")
	writeFile(t, dir, "b.txt")

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"empty", nil, nil},
		{"revision", []string{"main"}, []string{"main"}},
		{"staged", []string{"staged"}, []string{"staged"}},
		{"at path", []string{"@a.txt"}, []string{"--", "a.txt"}},
		{"bare path", []string{"a.txt"}, []string{"--", "a.txt"}},
		{"two paths", []string{"@a.txt", "b.txt"}, []string{"--", "a.txt", "b.txt"}},
		{"staged path", []string{"staged", "@a.txt"}, []string{"staged", "--", "a.txt"}},
		{"HEAD path", []string{"HEAD", "@a.txt"}, []string{"HEAD", "--", "a.txt"}},
		{"option path", []string{"-U5", "@a.txt"}, []string{"-U5", "--", "a.txt"}},
		{"range path", []string{"main..feat", "@a.txt"}, []string{"main..feat", "--", "a.txt"}},
		// A branch name may contain a slash, so an unknown bare name must stay a
		// revision instead of failing the existence check.
		{"branch with slash", []string{"feat/x"}, []string{"feat/x"}},
		{"passthrough", []string{"--", "git", "log"}, []string{"--", "git", "log"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeSpec(dir, tc.args)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	_, err := NormalizeSpec(dir, []string{"@nope.txt"})
	require.Error(t, err)
	assert.Equal(t, "no such path: nope.txt", err.Error())
}

func TestLoadGitAtPaths(t *testing.T) {
	dir := newGitRepo(t)
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("old\n"), 0o644))
	}
	runGit(t, dir, "git", "add", ".")
	runGit(t, dir, "git", "commit", "-m", "init")
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("new\n"), 0o644))
	}

	text, err := LoadGit(t.Context(), dir, []string{"@a.txt"})
	require.NoError(t, err)
	assert.Contains(t, text, "a/a.txt")
	assert.NotContains(t, text, "b/b.txt")
	assert.NotContains(t, text, "c/c.txt")

	text, err = LoadGit(t.Context(), dir, []string{"@a.txt", "@b.txt"})
	require.NoError(t, err)
	assert.Contains(t, text, "a/a.txt")
	assert.Contains(t, text, "b/b.txt")
	assert.NotContains(t, text, "c/c.txt")

	// HEAD plus a path keeps the single-file scope instead of printing HEAD.
	text, err = LoadGit(t.Context(), dir, []string{"HEAD", "@a.txt"})
	require.NoError(t, err)
	assert.Contains(t, text, "a/a.txt")
	assert.NotContains(t, text, "b/b.txt")
	assert.NotContains(t, text, "commit ")

	// A bare path keeps working, and `/diff -- <argv>` still passes through.
	text, err = LoadGit(t.Context(), dir, []string{"c.txt"})
	require.NoError(t, err)
	assert.Contains(t, text, "a/c.txt")
	assert.NotContains(t, text, "a/a.txt")
	text, err = LoadGit(t.Context(), dir, []string{"--", "git", "diff", "--name-only", "--", "c.txt"})
	require.NoError(t, err)
	// `--name-only` ran as given: a path list, not a unified diff. Comparing the
	// whole body would fail on git's stderr warnings.
	assert.Contains(t, text, "c.txt\n")
	assert.NotContains(t, text, "diff --git")
}

func TestEmptyNoteUntracked(t *testing.T) {
	dir := newGitRepo(t)
	writeFile(t, dir, "tracked.txt")
	writeFile(t, dir, "new.txt")
	runGit(t, dir, "git", "add", "tracked.txt")
	runGit(t, dir, "git", "commit", "-m", "init")

	note := EmptyNote(dir, []string{"@new.txt"})
	assert.Contains(t, note, "new.txt is untracked")
	assert.Contains(t, note, "git add -N new.txt")
	assert.Contains(t, EmptyNote(dir, []string{"@tracked.txt"}), "No changes vs tracked.txt")
	// `/diff -- <argv>` is not a path request: `git` is not an untracked file.
	assert.NotContains(t, EmptyNote(dir, []string{"--", "git", "status"}), "is untracked")
}
