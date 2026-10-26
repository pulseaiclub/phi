package bashcomplete

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The common-subcommand table seeds argument completion before the project
// history has run the command: "git che" completes checkout on day one.
func TestCompleteArgumentSeededFromCommonSubcommands(t *testing.T) {
	c := newCompleter(nil, nil, t.TempDir())

	assert.Equal(t, []Suggestion{
		{Command: "checkout", Insert: "checkout ", Source: SourceCommon, Score: 1},
	}, commonSourceRows(c.Complete("git che")))
}

// With no prefix to narrow it, the table lists its most-typed words first —
// every word of it, now that the list scrolls instead of truncating.
func TestCommonWordsKeepTableOrder(t *testing.T) {
	c := newCompleter(nil, nil, t.TempDir())

	rows := commonSourceRows(c.Complete("git "))
	assert.Equal(t, []string{
		"add", "checkout", "commit", "status", "push", "pull", "branch",
		"diff", "log", "merge", "rebase", "restore", "stash", "switch", "tag",
	}, commands(rows))
}

// Once history knows the command it is the only argument source: rows the
// project never ran do not crowd out the ones it did.
func TestHistoryBeatsTheCommonTable(t *testing.T) {
	c := newCompleter([]string{"git status"}, nil, t.TempDir())

	rows := c.Complete("git s")
	assert.Empty(t, commonSourceRows(rows), "the table stays behind history")
	for _, row := range rows {
		if row.Source == SourceArgument {
			assert.Equal(t, "status", row.Command)
		}
	}
}

// The table only knows the commands worth seeding; anything else stays quiet.
func TestCommonWordsStayQuietForUnknownCommands(t *testing.T) {
	c := newCompleter(nil, nil, t.TempDir())

	assert.Empty(t, commonSourceRows(c.Complete("make cle")))
}

// commands returns the row words of one source, in order.
func commands(rows []Suggestion) []string {
	words := make([]string, 0, len(rows))
	for _, row := range rows {
		words = append(words, row.Command)
	}
	return words
}

// commonSourceRows keeps only the common-source rows.
func commonSourceRows(rows []Suggestion) []Suggestion {
	var out []Suggestion
	for _, row := range rows {
		if row.Source == SourceCommon {
			out = append(out, row)
		}
	}
	return out
}
