package spec

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitRepo makes a temporary repository with two commits, so the commit
// generator has something real to read.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"commit", "--allow-empty", "-q", "-m", "first commit"},
		{"commit", "--allow-empty", "-q", "-m", "second commit"},
		{"branch", "feature/one"},
	} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	return dir
}

func TestArgumentPicksTheSlotTheCursorIsIn(t *testing.T) {
	table := Builtin()

	for _, tt := range []struct {
		name string
		args []string
		ok   bool
	}{
		{"the word being typed is the commit", []string{"log", "23"}, true},
		{"an empty word after log is the commit", []string{"log", ""}, true},
		{"a flag does not take the slot", []string{"log", "--oneline", "23"}, true},
		{"a flag value is not the slot", []string{"log", "-n", "5", ""}, true},
		{"past the declared argument there is nothing", []string{"log", "abc", "def"}, false},
		{"a subcommand without arguments has none", []string{"status", ""}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, read, ok := table.Argument("git", tt.args)
			assert.Equal(t, tt.ok, ok)
			if ok {
				assert.NotNil(t, read)
			}
		})
	}
}

func TestArgumentOfACommandWithoutGenerators(t *testing.T) {
	_, _, ok := Builtin().Argument("git", []string{"status", ""})
	assert.False(t, ok, "status declares no argument to read")
}

func TestCommitsReadsTheRepository(t *testing.T) {
	dir := gitRepo(t)

	rows := commits(t.Context(), dir, "")

	require.Len(t, rows, 2, "two commits")
	assert.Equal(t, "second commit", rows[0].Description, "newest first")
	assert.Equal(t, "first commit", rows[1].Description)
	assert.NotEmpty(t, rows[0].Name)
	assert.Len(t, rows[0].Name, 7, "the short hash is what a user types")
	assert.Equal(t, rows[0].Name+" ", rows[0].Insert)
}

func TestCommitsNarrowsToTheTypedPrefix(t *testing.T) {
	dir := gitRepo(t)
	all := commits(t.Context(), dir, "")
	require.Len(t, all, 2)

	rows := commits(t.Context(), dir, all[1].Name)

	require.Len(t, rows, 1)
	assert.Equal(t, all[1].Name, rows[0].Name)
}

func TestBranchesReadsLocalRefs(t *testing.T) {
	dir := gitRepo(t)

	rows := branches(t.Context(), dir, "")

	var names []string
	for _, row := range rows {
		names = append(names, row.Name)
	}
	assert.ElementsMatch(t, []string{"main", "feature/one"}, names)
}

// Neither a missing repository nor a directory that is not one is an error: a
// completion with no rows is the right answer for both.
func TestGeneratorsStayQuietOutsideARepository(t *testing.T) {
	dir := t.TempDir()

	assert.Empty(t, commits(t.Context(), dir, ""))
	assert.Empty(t, branches(t.Context(), dir, ""))
}

func TestValuesServeANarrowerPrefixFromAWiderRead(t *testing.T) {
	dir := gitRepo(t)
	values := NewValues(dir)
	read := generator("commits")
	require.NotNil(t, read)

	all := values.Load(t.Context(), "commits", "", read)
	require.Len(t, all, 2)

	rows := values.Rows("commits", all[0].Name[:4])

	require.Len(t, rows, 1, "the wider read answered, with no second git run")
	assert.Equal(t, all[0].Name, rows[0].Name)
}

func TestValuesRefreshFillsTheCache(t *testing.T) {
	dir := gitRepo(t)
	values := NewValues(dir)
	read := generator("commits")

	values.Refresh(t.Context(), "commits", "", read)

	require.Eventually(t, func() bool {
		return len(values.Rows("commits", "")) == 2
	}, 5*time.Second, 20*time.Millisecond)
}

// The cache key includes the prefix the read was made for, so a prefix that was
// never read does not borrow a narrower answer.
func TestValuesMissAnUnrelatedPrefix(t *testing.T) {
	values := NewValues(t.TempDir())
	values.store(valuesKey{"commits", "abc"}, []Candidate{{Name: "abc123"}}, true)

	assert.Empty(t, values.Rows("commits", "zzz"), "no cached list starts with zzz")
}

func TestCachePathIsPerCommand(t *testing.T) {
	dir := t.TempDir()
	assert.Equal(t, filepath.Join(dir, "help-git.json"), NewDiscoverer(dir).cachePath("Git"))
}
