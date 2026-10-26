package bashcomplete

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Deeper in the line the node's own subcommands still complete: `git remote `
// lists what remote offers, not git's top level.
func TestCompleteNestedNodeAtDeeperPositions(t *testing.T) {
	c := newCompleter(blankHistorySource(), nil, t.TempDir())

	rows := specRowsOnly(c.Complete("git remote "))

	require.NotEmpty(t, rows)
	assert.Contains(t, commands(rows), "set-url")
	for _, row := range rows {
		assert.NotEqual(t, "commit", row.Command, "git's top level does not leak in")
	}
}

func specRowsOnly(rows []Suggestion) []Suggestion {
	var out []Suggestion
	for _, row := range rows {
		if row.Source == SourceSpec {
			out = append(out, row)
		}
	}
	return out
}
