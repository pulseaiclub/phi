package bashcomplete

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blankHistorySource is a history source with nothing in it: the answers come
// from the spec table alone, so the assertions are about the table.
func blankHistorySource() []string { return nil }

// specRows keeps only the spec rows.
func specRows(rows []Suggestion) []Suggestion {
	var out []Suggestion
	for _, row := range rows {
		if row.Source == SourceSpec {
			out = append(out, row)
		}
	}
	return out
}

// A declared subcommand completes before the project has run the command, and
// carries the description the picker shows.
func TestCompleteSubcommandFromSpec(t *testing.T) {
	c := newCompleter(blankHistorySource(), nil, t.TempDir())

	rows := specRows(c.Complete("git swi"))

	require.Len(t, rows, 1)
	assert.Equal(t, "switch", rows[0].Command)
	assert.Equal(t, "switch", rows[0].Insert, "a subcommand leaves room for its arguments")
	assert.Equal(t, "switch branches", rows[0].Description)
}

// The declared subcommands lead; words the project already ran follow and are
// not dropped, so history still reaches a command the table does not know.
func TestCompleteSubcommandsLeadHistory(t *testing.T) {
	c := newCompleter([]string{"git status"}, nil, t.TempDir())

	rows := c.Complete("git sta")

	require.NotEmpty(t, rows)
	assert.Equal(t, SourceHistory, rows[0].Source, "whole lines stay the strongest rows")
	var wordRows []Suggestion
	for _, row := range rows {
		if row.Command == "status" && row.Insert != "" {
			wordRows = append(wordRows, row)
		}
	}
	require.Len(t, wordRows, 1, "one row per word: the spec row wins the dedupe")
	assert.Equal(t, SourceSpec, wordRows[0].Source, "the declared subcommands come first among word rows")

	ordered := commands(sourceRows(rows, SourceArgument))
	assert.NotContains(t, ordered, "status", "the declared row replaced the history word, not joined it")
}

// A flag completes from the table before history has run the command; the
// description says what it takes.
func TestCompleteOptionFromSpec(t *testing.T) {
	c := newCompleter(blankHistorySource(), nil, t.TempDir())

	rows := specRows(c.Complete("git commit --mes"))

	require.Len(t, rows, 1)
	assert.Equal(t, "--message", rows[0].Command)
	assert.Equal(t, "--message ", rows[0].Insert)
	assert.Equal(t, "<message> · use the given text as the commit message", rows[0].Description)
}

// Both spellings of one flag complete, longest first, so the short alias does
// not crowd out the long name. A bare dash is the word that asks for flags.
func TestCompleteOptionOffersEverySpelling(t *testing.T) {
	c := newCompleter(blankHistorySource(), nil, t.TempDir())

	rows := specRows(c.Complete("git commit -"))

	var names []string
	for _, row := range rows {
		if row.Command == "--amend" || row.Command == "-a" || row.Command == "--all" {
			names = append(names, row.Command)
		}
	}
	assert.Equal(t, []string{"--amend", "--all", "-a"}, names)
}

// Flags the project already ran complete too, after the declared ones.
func TestCompleteOptionMergesHistory(t *testing.T) {
	c := newCompleter([]string{"git --no-pager log"}, nil, t.TempDir())

	rows := c.Complete("git --no-p")

	var sources []Source
	for _, row := range rows {
		if row.Command == "--no-pager" {
			sources = append(sources, row.Source)
		}
	}
	assert.Equal(t, []Source{SourceSpec}, sources, "one row per word: the spec row wins the dedupe")
}

// A command with no spec is untouched: history and the built-in table answer
// exactly as they did before specs existed.
func TestCompleteWithoutSpecFallsBackToHistory(t *testing.T) {
	c := newCompleter([]string{"make clean", "make build"}, nil, t.TempDir())

	rows := c.Complete("make cle")

	assert.Empty(t, specRows(rows), "no spec knows make")
	assert.Equal(t, []string{"clean"}, commands(sourceRows(rows, SourceArgument)))
}

// The spec is skipped once a word names a positional argument the command
// takes itself, so flags do not bury the files a command wants.
func TestCompleteSpecStopsAtPositionalArgument(t *testing.T) {
	c := newCompleter(blankHistorySource(), nil, t.TempDir())

	assert.Empty(t, specRows(c.Complete("git add -p README.md -")), "add takes paths after its first argument")
	assert.NotEmpty(t, specRows(c.Complete("git commit README.md -")), "commit keeps completing flags")
}
