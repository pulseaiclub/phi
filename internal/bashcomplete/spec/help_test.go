package spec

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cobraUsage is the shape a cobra command prints: a section header, then one
// flag per line with its description indented under it when it wraps.
const cobraUsage = `Usage:
  tea [command]

Available Commands:
  build       Build the binary
  serve       Serve the API
  version     Print the version

Flags:
  -h, --help            help for tea
  -v, --verbose         print what is being done
      --config string   read this config file (default "tea.yaml")
      --mode string     run in this mode; one of:
                          dev, prod
  -o, --output <FILE>   write the result here

Use "tea [command] --help" for more information about a command.
`

// flagPackageUsage is the shape a Go flag package prints: the flag, then its
// signature and description, with no section header at all.
const flagPackageUsage = `Usage of phi:
  -config string
    	path to the config file
  -debug
    	print debug output
  -timeout duration
    	give up after this long (default 30s)
`

func TestFromHelpReadsCobraFlags(t *testing.T) {
	node := FromHelp("tea", cobraUsage)

	require.NotEmpty(t, node.Options)
	byName := optionsByName(node)
	assert.Equal(t, "help for tea", byName["--help"].Description)
	assert.Equal(t, "print what is being done", byName["--verbose"].Description)
	assert.Equal(t, "read this config file (default \"tea.yaml\")", byName["--config"].Description)
	assert.Equal(t, "help for tea", byName["-h"].Description, "the short spelling carries the description too")
}

func TestFromHelpKeepsOneSpellingListPerFlag(t *testing.T) {
	node := FromHelp("tea", cobraUsage)

	var spellings []string
	for _, opt := range node.Options {
		if opt.Description != "help for tea" {
			continue
		}
		spellings = append(spellings, opt.Names...)
	}
	assert.Equal(t, []string{"-h", "--help"}, spellings, "one line, one flag, both spellings")
}

func TestFromHelpJoinsWrappedDescriptions(t *testing.T) {
	node := FromHelp("tea", cobraUsage)

	assert.Equal(t, `run in this mode; one of: dev, prod`, optionsByName(node)["--mode"].Description)
}

func TestFromHelpSplitsAnAttachedValue(t *testing.T) {
	node := FromHelp("tea", cobraUsage)

	output := optionsByName(node)["--output"]
	require.Len(t, output.Args, 1)
	assert.Equal(t, "FILE", output.Args[0].Name)
	assert.Equal(t, "write the result here", output.Description)
}

func TestFromHelpReadsGoFlagStyle(t *testing.T) {
	node := FromHelp("phi", flagPackageUsage)

	byName := optionsByName(node)
	require.Contains(t, byName, "-config", "a single-dash tool keeps its single-dash spelling")
	assert.Equal(t, "path to the config file", byName["-config"].Description)
	assert.Equal(t, []Arg{{Name: "string"}}, byName["-config"].Args)
	assert.Equal(t, "print debug output", byName["-debug"].Description)
	assert.Equal(t, "give up after this long (default 30s)", byName["-timeout"].Description)
}

func TestFromHelpReadsSubcommands(t *testing.T) {
	node := FromHelp("tea", cobraUsage)

	var names []string
	for _, child := range node.Subcommands {
		names = append(names, child.Name)
	}
	assert.Equal(t, []string{"build", "serve", "version"}, names)
}

func TestFromHelpWithoutFlagsIsEmpty(t *testing.T) {
	assert.Equal(t, Command{}, FromHelp("echo", "echo a line\n"))
	assert.Equal(t, Command{}, FromHelp("true", ""))
}

// optionsByName indexes a node's flags by every spelling, pointing at a copy so
// the assertions read like the picker would.
func optionsByName(node Command) map[string]Option {
	out := make(map[string]Option, len(node.Options))
	for _, opt := range node.Options {
		for _, name := range opt.Names {
			out[name] = opt
		}
	}
	return out
}
