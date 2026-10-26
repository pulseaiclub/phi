package bashcomplete

import (
	"context"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/bashcomplete/spec"
)

// helpOutput is a usage table in the shape command-line libraries print.
const helpOutput = `Flags:
      --verbose   print what is being done
  -v            print what is being done
`

// runs returns the commands whose help was read, safe against the background
// read appending while a test asks.
func (f *fakeDiscovery) runs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ran...)
}

// fakeDiscovery is a discoverer whose --help output is fixed, so the test
// never spawns a process.
type fakeDiscovery struct {
	*spec.Discoverer
	mu  sync.Mutex
	ran []string
}

func newFakeDiscovery(t *testing.T) *fakeDiscovery {
	t.Helper()
	fake := &fakeDiscovery{}
	fake.Discoverer = spec.NewDiscoverer(t.TempDir())
	fake.Cached = spec.NewTable
	fake.Lookup = func(name string) (string, error) { return "/bin/" + name, nil }
	fake.Run = func(_ context.Context, name string) (string, error) {
		fake.mu.Lock()
		fake.ran = append(fake.ran, name)
		fake.mu.Unlock()
		return helpOutput, nil
	}
	return fake
}

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
// not dropped, so history still reaches a command the table does not know. The
// word the project ran is one row: the declared one, which carries the
// description and leaves room for arguments.
func TestCompleteSubcommandsLeadHistory(t *testing.T) {
	c := newCompleter([]string{"git status"}, nil, t.TempDir())

	rows := c.Complete("git sta")

	require.NotEmpty(t, rows)
	assert.Equal(t, SourceSpec, rows[0].Source, "the declared subcommands come first")
	assert.Equal(t, "status", rows[0].Command)
	assert.Equal(t, "show the work tree status", rows[0].Description)

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

// A command the table does not know starts a background read once its word is
// finished, and the keystroke that starts it still answers from what is known.
func TestCompleteStartsDiscoveryForAnUnknownCommand(t *testing.T) {
	help := newFakeDiscovery(t)
	c := newCompleter([]string{"tea build"}, nil, t.TempDir())
	c.SetDiscovery(help.Discoverer)

	assert.Empty(t, c.Complete("tea --ver"), "nothing is known yet, and nothing waits")
	require.Eventually(t, func() bool {
		return len(c.Complete("tea --ver")) > 0
	}, 2*time.Second, 10*time.Millisecond, "the read lands and the flags complete")
}

// The word has to be finished: a half-typed command names nothing to run, so
// typing it costs no process.
func TestCompleteDoesNotDiscoverACommandStillBeingTyped(t *testing.T) {
	help := newFakeDiscovery(t)
	c := newCompleter(blankHistorySource(), nil, t.TempDir())
	c.SetDiscovery(help.Discoverer)

	c.Complete("te")

	time.Sleep(50 * time.Millisecond)
	assert.Empty(t, help.runs(), "no process for a command that is not finished")
}

// A path word completes from the filesystem and is never executed.
func TestCompleteDoesNotDiscoverPaths(t *testing.T) {
	help := newFakeDiscovery(t)
	c := newCompleter(blankHistorySource(), nil, t.TempDir())
	c.SetDiscovery(help.Discoverer)

	c.Complete("./tea --ver")

	time.Sleep(50 * time.Millisecond)
	assert.Empty(t, help.runs())
}

// A positional argument completes from the project itself once the read lands:
// `git log ` lists this repository's commits, hash first and subject after.
func TestCompleteArgumentValuesFromTheRepository(t *testing.T) {
	dir := gitRepoIn(t, "second commit", "first commit")
	c := newCompleter(blankHistorySource(), nil, dir)
	c.SetValues(spec.NewValues(dir))

	assert.Empty(t, c.Complete("git log "), "the first keystroke starts the read")

	require.Eventually(t, func() bool {
		return len(c.Complete("git log ")) > 0
	}, 5*time.Second, 20*time.Millisecond)

	rows := c.Complete("git log ")
	require.Len(t, rows, 2, "both commits of the repository")
	var subjects []string
	for _, row := range rows {
		subjects = append(subjects, row.Description)
		assert.Len(t, row.Command, 7, "the short hash is the word a user types")
	}
	assert.ElementsMatch(t, []string{"first commit", "second commit"}, subjects)
}

// Branch names complete where a checkout or a switch expects one.
func TestCompleteBranchValues(t *testing.T) {
	dir := gitRepoIn(t, "only commit")
	c := newCompleter(blankHistorySource(), nil, dir)
	c.SetValues(spec.NewValues(dir))
	c.values.Load(t.Context(), "branches", "", readBranches(t))

	rows := c.Complete("git switch ")

	require.NotEmpty(t, rows)
	assert.Equal(t, "main", rows[0].Command)
}

// gitRepoIn makes a temporary repository with the given commit subjects, so a
// test can read back what the generator should list.
func gitRepoIn(t *testing.T, subjects ...string) string {
	t.Helper()
	dir := t.TempDir()
	steps := [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	}
	for _, subject := range subjects {
		steps = append(steps, []string{"commit", "--allow-empty", "-q", "-m", subject})
	}
	for _, args := range steps {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	return dir
}

// readBranches is the branch reader the spec registry exposes, for a test that
// wants to prime the cache without waiting on a background read.
func readBranches(t *testing.T) spec.Generator {
	t.Helper()
	read, ok := spec.GeneratorByName("branches")
	require.True(t, ok)
	return read
}
