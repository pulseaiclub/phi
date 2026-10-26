package composer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/session/shellhist"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// historyStore writes commands to a real shell-history store, oldest first, so
// the predictor is exercised over the file layout it reads in production.
func historyStore(t *testing.T, commands ...string) *shellhist.Store {
	t.Helper()
	store := shellhist.New(t.TempDir())
	for i, command := range commands {
		require.NoError(t, store.Append(shellhist.Entry{
			Version: 1,
			At:      int64(i + 1),
			Cwd:     "/tmp",
			Command: command,
		}))
	}
	return store
}

// newPredictor wires a predictor over fixed sources, so ranking is asserted
// without depending on the machine's PATH or directories.
func newPredictor(t *testing.T, store *shellhist.Store, commands ...string) *LocalBashPredictor {
	t.Helper()
	predictor := NewLocalBashPredictor(store, t.TempDir())
	predictor.commands = func() []string { return commands }
	return predictor
}

// A command word completes from two sources: whole lines from history first,
// then executable names from PATH.
func TestPredictCombinesHistoryAndPath(t *testing.T) {
	predictor := newPredictor(t, historyStore(t, "git status", "git stash", "npm test"), "git", "ginkgo")

	items, err := predictor.Predict(t.Context(), "gi")

	require.NoError(t, err)
	assert.Equal(t, []controller.BashSuggestion{
		{Command: "git stash", Source: "history", Score: 1.0},
		{Command: "git status", Source: "history", Score: 0.5},
		{Command: "git", Insert: "git ", Source: "command", Score: 1},
		{Command: "ginkgo", Insert: "ginkgo ", Source: "command", Score: 1},
	}, items)
}

// An argument completes with words seen in the same position of the same
// command, ranked by how often the project ran them.
func TestPredictCompletesArgumentsFromHistory(t *testing.T) {
	predictor := newPredictor(t, historyStore(t, "git status", "git stash", "git status"))

	items, err := predictor.Predict(t.Context(), "git sta")

	require.NoError(t, err)
	assert.Equal(t, []controller.BashSuggestion{
		{Command: "git status", Source: "history", Score: 1.0},
		{Command: "git stash", Source: "history", Score: 0.5},
		{Command: "status", Insert: "status ", Source: "argument", Score: 1.0},
		{Command: "stash", Insert: "stash ", Source: "argument", Score: 0.5},
	}, items)
}

// A path word completes against the project directory.
func TestPredictCompletesPaths(t *testing.T) {
	cwd := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(cwd, "doc"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "doc", "note.md"), nil, 0o644))
	predictor := NewLocalBashPredictor(historyStore(t), cwd)

	items, err := predictor.Predict(t.Context(), "./doc/")

	require.NoError(t, err)
	assert.Equal(t, []controller.BashSuggestion{
		{Command: "./doc/note.md", Insert: "./doc/note.md ", Source: "path", Score: 1},
	}, items)
}

// The picker lists at most bashSuggestVisible rows, strongest first: history
// rows lead, and the overflow is cut from the end.
func TestPredictCapsTheList(t *testing.T) {
	commands := []string{"goa", "gob", "goc", "god", "goe", "gof", "gog", "goh"}
	history := []string{"go one", "go two", "go three", "go four", "go five", "go six"}
	predictor := newPredictor(t, historyStore(t, history...), commands...)

	items, err := predictor.Predict(t.Context(), "go")

	require.NoError(t, err)
	require.Len(t, items, bashSuggestVisible)
	assert.Equal(t, "go six", items[0].Command, "the newest history line leads the capped list")
	assert.Equal(t, "goe", items[bashSuggestVisible-1].Command, "the overflow is cut from the end")
}

// A bare bang is not a query: whitespace-only text completes nothing.
func TestPredictIgnoresBlankText(t *testing.T) {
	predictor := newPredictor(t, historyStore(t, "git stash"), "git")

	items, err := predictor.Predict(t.Context(), "   ")

	require.NoError(t, err)
	assert.Empty(t, items)
}

// Without a history store the word-level sources still complete: a project
// without a session directory keeps PATH and path completion.
func TestPredictWithoutAStoreStillCompletesCommands(t *testing.T) {
	predictor := newPredictor(t, nil, "git", "ginkgo")

	items, err := predictor.Predict(t.Context(), "gin")

	require.NoError(t, err)
	assert.Equal(t, []controller.BashSuggestion{
		{Command: "ginkgo", Insert: "ginkgo ", Source: "command", Score: 1},
	}, items)
}

// An inert predictor is not an error: the composer installs one only when it
// has an engine, and must never panic if it does not.
func TestPredictOnNilPredictorIsInert(t *testing.T) {
	var predictor *LocalBashPredictor

	items, err := predictor.Predict(t.Context(), "git")

	require.NoError(t, err)
	assert.Empty(t, items)
}
