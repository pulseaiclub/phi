package spec

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuiltinParsesEverySpecFile(t *testing.T) {
	table := Builtin()

	require.NotNil(t, table)
	for _, name := range []string{"git", "go", "docker", "npm", "cargo"} {
		_, ok := table.Lookup(name, nil)
		assert.True(t, ok, "%s should have a spec", name)
	}
}

func TestLookupWalksSubcommands(t *testing.T) {
	table := Builtin()

	node, ok := table.Lookup("git", []string{"remote", "add"})
	require.True(t, ok)
	assert.Equal(t, "add", node.Name)
	assert.Contains(t, optionNames(node), "--fetch", "the node is the add subcommand, not the root")
}

func TestLookupStopsAtAnUnknownSubcommand(t *testing.T) {
	table := Builtin()

	node, ok := table.Lookup("git", []string{"status", "feature"})
	require.True(t, ok)
	assert.Equal(t, "status", node.Name, "an argument past the subcommand stays on it")
}

func TestLookupSkipsFlags(t *testing.T) {
	table := Builtin()

	node, ok := table.Lookup("git", []string{"--no-pager", "commit"})
	require.True(t, ok)
	assert.Equal(t, "commit", node.Name)
}

func TestLookupUnknownCommand(t *testing.T) {
	_, ok := Builtin().Lookup("definitely-not-a-command", nil)
	assert.False(t, ok)
}

func TestCompleteOffersSubcommandsAndOptions(t *testing.T) {
	rows := Builtin().Complete("git", nil, "sta")

	var names []string
	for _, row := range rows {
		names = append(names, row.Name)
	}
	assert.ElementsMatch(t, []string{"stash", "status"}, names)
}

func TestCompleteOptionDescribesItsValue(t *testing.T) {
	rows := Builtin().Complete("git", []string{"commit"}, "--mes")

	require.Len(t, rows, 1)
	assert.Equal(t, "--message", rows[0].Name)
	assert.Equal(t, "<message> · use the given text as the commit message", rows[0].Description)
	assert.Equal(t, KindOption, rows[0].Kind)
}

func TestCompleteSpellingsLongestFirst(t *testing.T) {
	rows := Builtin().Complete("git", []string{"commit"}, "--am")

	require.Len(t, rows, 1)
	assert.Equal(t, "--amend", rows[0].Name)
}

func TestCompleteOffersAFoldedFullMatch(t *testing.T) {
	rows := Builtin().Complete("git", nil, "SWITCH")

	require.Len(t, rows, 1)
	assert.Equal(t, "switch", rows[0].Name, "a folded match still completes, with its real spelling")
}

func TestCompleteRespectsNoOptionAfterArgument(t *testing.T) {
	assert.Empty(t, Builtin().Complete("git", []string{"add", "README.md"}, "-"), "add takes paths")
	assert.NotEmpty(t, Builtin().Complete("git", []string{"commit", "README.md"}, "-"), "commit takes flags")
}

func TestCompleteTypedWordIsNotOffered(t *testing.T) {
	assert.Empty(t, Builtin().Complete("git", nil, "status"))
}

// optionNames lists the spellings a node declares, for assertions about which
// node a lookup landed on.
func optionNames(node Command) []string {
	var names []string
	for _, opt := range node.Options {
		names = append(names, opt.Names...)
	}
	return names
}
