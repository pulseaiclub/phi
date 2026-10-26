package bashcomplete

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newCompleter wires a completer over fixed sources, so ranking is asserted
// without touching PATH or the real home directory.
func newCompleter(history, commands []string, cwd string) *Completer {
	return New(
		func() []string { return history },
		func() []string { return commands },
		cwd,
	)
}

func TestCompleteCommandWordCombinesHistoryAndPath(t *testing.T) {
	c := newCompleter(
		[]string{"git status", "git stash", "npm test"},
		[]string{"docker", "git", "ginkgo"},
		t.TempDir(),
	)

	assert.Equal(t, []Suggestion{
		{Command: "git stash", Source: SourceHistory, Score: 1.0},
		{Command: "git status", Source: SourceHistory, Score: 0.5},
		{Command: "git", Insert: "git ", Source: SourceCommand, Score: 1},
		{Command: "ginkgo", Insert: "ginkgo ", Source: SourceCommand, Score: 1},
	}, c.Complete("gi"))
}

func TestCompleteCommandWordWithoutHistoryMatch(t *testing.T) {
	c := newCompleter([]string{"git status"}, []string{"docker"}, t.TempDir())

	assert.Equal(t, []Suggestion{
		{Command: "docker", Insert: "docker ", Source: SourceCommand, Score: 1},
	}, c.Complete("do"))
}

// A word equal to the typed text completes nothing, whichever source it came
// from.
func TestCompleteExcludesTheTypedWordItself(t *testing.T) {
	c := newCompleter([]string{"git status"}, []string{"git"}, t.TempDir())

	assert.Empty(t, c.Complete("git status"))
}

// History lines and command names can draw the same row; the history row is
// the stronger one and the duplicate is dropped.
func TestCompleteDedupesAcrossSources(t *testing.T) {
	c := newCompleter([]string{"git"}, []string{"git"}, t.TempDir())

	assert.Equal(t, []Suggestion{
		{Command: "git", Source: SourceHistory, Score: 1.0},
	}, c.Complete("gi"))
}

func TestCompleteArgumentRanksFrequencyThenRecency(t *testing.T) {
	c := newCompleter(
		[]string{"git status", "git stash", "git status", "docker ps", "git add"},
		nil,
		t.TempDir(),
	)

	assert.Equal(t, []Suggestion{
		{Command: "git status", Source: SourceHistory, Score: 1.0},
		{Command: "git stash", Source: SourceHistory, Score: 0.5},
		{Command: "status", Insert: "status ", Source: SourceArgument, Score: 1.0},
		{Command: "stash", Insert: "stash ", Source: SourceArgument, Score: 0.5},
	}, c.Complete("git sta"))
}

// A word after a space is a fresh argument: every word seen in that position is
// offered, with no prefix to narrow it.
func TestCompleteArgumentAfterASpaceListsEveryWord(t *testing.T) {
	c := newCompleter([]string{"git status", "git push", "git status"}, nil, t.TempDir())

	assert.Equal(t, []Suggestion{
		{Command: "git status", Source: SourceHistory, Score: 1.0},
		{Command: "git push", Source: SourceHistory, Score: 0.5},
		{Command: "status", Insert: "status ", Source: SourceArgument, Score: 1.0},
		{Command: "push", Insert: "push ", Source: SourceArgument, Score: 0.5},
	}, c.Complete("git "))
}

// Arguments are matched per command: git's subcommands do not complete make's,
// and the common table has nothing to say about make either.
func TestCompleteArgumentMatchesTheCommandWord(t *testing.T) {
	c := newCompleter([]string{"git status"}, nil, t.TempDir())

	assert.Empty(t, c.Complete("make sta"))
}

func TestCompletePathEntries(t *testing.T) {
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "go.mod"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "script.sh"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, ".gitignore"), nil, 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(cwd, "doc"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "doc", "note.md"), nil, 0o644))
	c := newCompleter(nil, nil, cwd)

	t.Run("typed prefix narrows the entries", func(t *testing.T) {
		assert.Equal(t, []Suggestion{
			{Command: "./script.sh", Insert: "./script.sh ", Source: SourcePath, Score: 1},
		}, c.Complete("./sc"))
	})

	t.Run("a directory completes with a trailing separator", func(t *testing.T) {
		assert.Equal(t, []Suggestion{
			{Command: "./doc/", Insert: "./doc/", Source: SourcePath, Score: 1},
		}, c.Complete("./do"))
	})

	t.Run("an empty base lists the directory", func(t *testing.T) {
		assert.Equal(t, []Suggestion{
			{Command: "./doc/", Insert: "./doc/", Source: SourcePath, Score: 1},
			{Command: "./go.mod", Insert: "./go.mod ", Source: SourcePath, Score: 1},
			{Command: "./script.sh", Insert: "./script.sh ", Source: SourcePath, Score: 1},
		}, c.Complete("./"))
	})

	t.Run("a relative directory continues inside it", func(t *testing.T) {
		assert.Equal(t, []Suggestion{
			{Command: "doc/note.md", Insert: "doc/note.md ", Source: SourcePath, Score: 1},
		}, c.Complete("doc/"))
	})

	t.Run("dotfiles need a dotted base", func(t *testing.T) {
		assert.Equal(t, []Suggestion{
			{Command: "./.gitignore", Insert: "./.gitignore ", Source: SourcePath, Score: 1},
		}, c.Complete("./.g"))
	})

	t.Run("an unknown directory stays quiet", func(t *testing.T) {
		assert.Empty(t, c.Complete("missing/x"))
	})
}

func TestCompleteBlankTextIsEmpty(t *testing.T) {
	c := newCompleter([]string{"git status"}, []string{"git"}, t.TempDir())

	assert.Empty(t, c.Complete(""))
	assert.Empty(t, c.Complete("   "))
}

func TestCompleteWithoutSourcesIsEmpty(t *testing.T) {
	c := New(nil, nil, t.TempDir())
	// git would seed from the common table, so a command outside it shows the
	// sources themselves are what is empty.
	assert.Empty(t, c.Complete("make "))
}

// A nil completer is not a panic: callers install one unconditionally.
func TestCompleteOnNilCompleterIsEmpty(t *testing.T) {
	var c *Completer
	assert.Empty(t, c.Complete("git"))
}
