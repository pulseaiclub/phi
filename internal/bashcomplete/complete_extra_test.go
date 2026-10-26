package bashcomplete

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// commandsSource keeps every row of one source, in order.
func sourceRows(rows []Suggestion, source Source) []Suggestion {
	var out []Suggestion
	for _, row := range rows {
		if row.Source == source {
			out = append(out, row)
		}
	}
	return out
}

// A launcher in front of the command does not hide the command's own
// arguments: `sudo git sta` completes git's subcommands, not sudo's. The
// declared rows lead and the history words fold into them.
func TestCompleteReadsArgumentsPastALauncher(t *testing.T) {
	c := newCompleter([]string{"git status", "git stash"}, nil, t.TempDir())

	rows := c.Complete("sudo git sta")

	var words []string
	for _, row := range rows {
		if row.Source == SourceSpec || row.Source == SourceArgument {
			words = append(words, row.Command)
		}
	}
	assert.Equal(t, []string{"status", "stash"}, dedupeStrings(words), "declared rows, longest spelling first")
}

// An assignment in front of the command is configuration, not the command.
func TestCompleteReadsArgumentsPastAnAssignment(t *testing.T) {
	c := newCompleter([]string{"go test ./..."}, nil, t.TempDir())

	rows := c.Complete("GOOS=linux go te")

	var words []string
	for _, row := range rows {
		if row.Source == SourceSpec || row.Source == SourceArgument {
			words = append(words, row.Command)
		}
	}
	assert.Equal(t, []string{"test"}, words)
}

// dedupeStrings keeps first occurrences, in order.
func dedupeStrings(words []string) []string {
	seen := make(map[string]struct{}, len(words))
	out := make([]string, 0, len(words))
	for _, word := range words {
		if _, duplicate := seen[word]; duplicate {
			continue
		}
		seen[word] = struct{}{}
		out = append(out, word)
	}
	return out
}

// The spec table still knows a command reached through a launcher.
func TestCompleteSeedsSpecWordsPastALauncher(t *testing.T) {
	c := newCompleter(nil, nil, t.TempDir())

	rows := c.Complete("sudo git che")

	require.NotEmpty(t, rows)
	assert.Equal(t, "checkout", rows[0].Command)
	assert.Equal(t, SourceSpec, rows[0].Source)
}

// A flag the project ran a spec-less command with completes from history
// alone: no spec knows it, so history is the only source.
func TestCompleteFlagFromHistory(t *testing.T) {
	c := newCompleter([]string{"tea --frobnicate 'a b'", "tea build"}, nil, t.TempDir())

	rows := sourceRows(c.Complete("tea --fro"), SourceFlag)

	require.Equal(t, []string{"--frobnicate"}, commands(rows))
	assert.Equal(t, "--frobnicate ", rows[0].Insert)
}

// A flag with no history behind it and no spec above it offers nothing: a
// guess would be a word the project never typed.
func TestCompleteFlagWithoutHistoryIsQuiet(t *testing.T) {
	c := newCompleter([]string{"tea build"}, nil, t.TempDir())

	assert.Empty(t, c.Complete("tea --fro"))
}

// A bare word completes files in the project, where a shell would take them:
// the subcommand source comes first, the file rows follow.
func TestCompleteBareWordOffersProjectFiles(t *testing.T) {
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "main.go"), nil, 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(cwd, "main_dir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, ".hidden"), nil, 0o644))
	c := newCompleter([]string{"git merge"}, nil, cwd)

	rows := sourceRows(c.Complete("go mai"), SourcePath)

	assert.Equal(t, []string{"main.go", "main_dir/"}, commands(rows))
}

// A typed path already goes through the path source; the bare-name pass must
// not list the same entries again.
func TestCompleteTypedPathIsNotOfferedTwice(t *testing.T) {
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "run.sh"), nil, 0o644))
	c := newCompleter(nil, nil, cwd)

	assert.Equal(t, []string{"./run.sh"}, commands(c.Complete("./ru")))
}

func TestCompleteCommandWordsPreferTheLiteralPrefix(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case folding is a Windows rule")
	}
	names := []string{"Git"}
	for i := range wordLimit + 10 {
		names = append(names, fmt.Sprintf("GitTool%02d", i))
	}
	c := newCompleter(nil, names, t.TempDir())

	rows := sourceRows(c.Complete("git"), SourceCommand)

	require.Len(t, rows, wordLimit)
	assert.Equal(t, "Git", rows[0].Command, "the literal prefix ranks above the folded rows")
	assert.NotContains(t, commands(rows), "GitTool50", "the cut keeps the strongest rows, not the first ones")
}
