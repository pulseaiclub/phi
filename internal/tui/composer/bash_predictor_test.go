package composer

import (
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

// Completion is prefix-only: every command that literally starts with the typed
// text is listed, newest first, with a recency weight.
func TestPredictListsEveryPrefixMatch(t *testing.T) {
	predictor := NewBashHistoryPredictor(historyStore(t, "ls -la", "git stash", "git status"))

	items, err := predictor.Predict(t.Context(), "git s")

	require.NoError(t, err)
	assert.Equal(t, []controller.BashSuggestion{
		{Command: "git status", Score: 1.0, IsPrefix: true},
		{Command: "git stash", Score: 0.5, IsPrefix: true},
	}, items)
}

// The command equal to the typed text is excluded: there is nothing left to
// complete, so it never crowds the picker.
func TestPredictExcludesTheExactCommand(t *testing.T) {
	predictor := NewBashHistoryPredictor(historyStore(t, "git stash", "git status"))

	items, err := predictor.Predict(t.Context(), "git status")

	require.NoError(t, err)
	assert.Empty(t, items)
}

// Duplicates keep their most recent position: a command run twice is listed once.
func TestPredictDeduplicatesByMostRecent(t *testing.T) {
	predictor := NewBashHistoryPredictor(historyStore(t, "git status", "git stash", "git status"))

	items, err := predictor.Predict(t.Context(), "git")

	require.NoError(t, err)
	assert.Equal(t, []controller.BashSuggestion{
		{Command: "git status", Score: 1.0, IsPrefix: true},
		{Command: "git stash", Score: 0.5, IsPrefix: true},
	}, items)
}

// A non-matching prefix leaves the picker quiet rather than guessing.
func TestPredictStaysQuietWhenNothingMatches(t *testing.T) {
	predictor := NewBashHistoryPredictor(historyStore(t, "make build", "npm test"))

	items, err := predictor.Predict(t.Context(), "zzz")

	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestPredictWithoutHistoryIsSilent(t *testing.T) {
	predictor := NewBashHistoryPredictor(historyStore(t))

	items, err := predictor.Predict(t.Context(), "git")

	require.NoError(t, err)
	assert.Empty(t, items)
}

// A bare bang is not a query: whitespace-only text completes nothing.
func TestPredictIgnoresBlankText(t *testing.T) {
	predictor := NewBashHistoryPredictor(historyStore(t, "git stash"))

	items, err := predictor.Predict(t.Context(), "   ")

	require.NoError(t, err)
	assert.Empty(t, items)
}

// An inert predictor is not an error: the composer installs one only when it
// has a history directory, and must never panic if it does not.
func TestPredictWithoutAStoreIsInert(t *testing.T) {
	for _, tt := range []struct {
		name      string
		predictor *BashHistoryPredictor
	}{
		{"nil predictor", nil},
		{"no store", NewBashHistoryPredictor(nil)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			items, err := tt.predictor.Predict(t.Context(), "git")

			require.NoError(t, err)
			assert.Empty(t, items)
		})
	}
}

// prefixMatches walks history newest first, drops the exact match and
// duplicates, and caps the result.
func TestPrefixMatches(t *testing.T) {
	history := []string{"git pull", "git push", "npm test", "git push", "git status"}

	assert.Equal(t, []string{"git status", "git push", "git pull"},
		prefixMatches("git", history, 10))
	assert.Equal(t, []string{"git status", "git push"},
		prefixMatches("git", history, 2))
	assert.Empty(t, prefixMatches("git", history, 0))
	assert.Empty(t, prefixMatches("zzz", history, 10))
}

func TestRecencyScore(t *testing.T) {
	assert.Equal(t, 1.0, recencyScore(0, 1))
	assert.Equal(t, 1.0, recencyScore(0, 3))
	assert.InDelta(t, 0.67, recencyScore(1, 3), 0.01)
	assert.InDelta(t, 0.33, recencyScore(2, 3), 0.01)
}
