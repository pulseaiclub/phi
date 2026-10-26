package bashcomplete

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// commands returns the row words, in order.
func commands(rows []Suggestion) []string {
	words := make([]string, 0, len(rows))
	for _, row := range rows {
		words = append(words, row.Command)
	}
	return words
}

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

// A command name the history also ran is one row, from the source that can
// complete it: whole lines never complete, so they never crowd the list.
func TestCompleteDedupesAcrossSources(t *testing.T) {
	c := newCompleter([]string{"git"}, []string{"git"}, t.TempDir())

	assert.Equal(t, []Suggestion{
		{Command: "git", Insert: "git ", Source: SourceCommand, Score: 1},
	}, c.Complete("gi"))
}

// make has no spec, so the ranking here is history's alone: frequency first,
// most recent use breaking ties.
func TestCompleteArgumentRanksFrequencyThenRecency(t *testing.T) {
	c := newCompleter(
		[]string{"make bar", "make baz", "make bar", "docker ps", "make all"},
		nil,
		t.TempDir(),
	)

	assert.Equal(t, []Suggestion{
		{Command: "bar", Insert: "bar ", Source: SourceArgument, Score: 1.0},
		{Command: "baz", Insert: "baz ", Source: SourceArgument, Score: 0.5},
	}, c.Complete("make ba"))
}

// A word after a space is a fresh argument: every word seen in that position is
// offered, with no prefix to narrow it. make has no spec, so the word rows are
// history's alone.
func TestCompleteArgumentAfterASpaceListsEveryWord(t *testing.T) {
	c := newCompleter([]string{"make bar", "make baz", "make bar"}, nil, t.TempDir())

	assert.Equal(t, []Suggestion{
		{Command: "bar", Insert: "bar ", Source: SourceArgument, Score: 1.0},
		{Command: "baz", Insert: "baz ", Source: SourceArgument, Score: 0.5},
	}, c.Complete("make "))
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

// A typed line never completes whole from history: the part that differs is
// usually a one-off value such as a commit hash or a process id, so offering
// `git log 2343` back as a row would only teach a constant that never repeats.
func TestCompleteNeverOffersAWholeHistoryLine(t *testing.T) {
	c := newCompleter([]string{"git log 2343", "git log --oneline"}, []string{"git"}, t.TempDir())

	for _, typed := range []string{"git l", "git log", "git log 2"} {
		for _, row := range c.Complete(typed) {
			assert.NotEqual(t, "git log 2343", row.Command, "typed %q", typed)
			assert.NotEmpty(t, row.Insert, "every row completes a word, not a line")
		}
	}
}

// A command list runs several commands; the cursor's one is the only one worth
// completing, so the words before a separator never steer the answer.
func TestCompleteReadsOnlyTheCommandUnderTheCursor(t *testing.T) {
	c := newCompleter(blankHistorySource(), nil, t.TempDir())

	for _, typed := range []string{"cd doc && git sta", "ls | git sta", "cd doc; git sta", "serve & git sta"} {
		rows := c.Complete(typed)
		require.NotEmpty(t, rows, "typed %q", typed)
		assert.Contains(t, commands(rows), "stash", "typed %q", typed)
	}
}

// A flag after a separator belongs to the command after it, not to the one
// before: completing `cd doc && ` must not list go's flags.
func TestCompleteDoesNotBorrowTheFirstCommandsSpec(t *testing.T) {
	c := newCompleter(blankHistorySource(), nil, t.TempDir())

	rows := c.Complete("git commit -m x && go -")

	for _, row := range rows {
		assert.NotContains(t, row.Command, "--message", "the flag of the command before the separator")
	}
}
