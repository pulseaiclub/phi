package composer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pulseaiclub/sev"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func TestPredictGitCommandsAndFlags(t *testing.T) {
	predictor := NewBashPredictor(t.TempDir(), nil)
	for _, query := range []string{"git ch", "git log --"} {
		t.Run(query, func(t *testing.T) {
			items, err := predictor.Predict(t.Context(), query)
			require.NoError(t, err)
			require.NotEmpty(t, items)
			assert.LessOrEqual(t, len(items), bashSuggestVisible)
			for _, item := range items {
				assert.True(t, strings.HasPrefix(item.Command, query), item.Command)
				assert.True(t, item.IsPrefix)
				assert.Positive(t, item.Score)
				assert.NotEmpty(t, item.Description)
				assert.Equal(t, item.Description, bashSuggestionLabel(item))
			}
		})
	}
}

func TestPredictDistinguishesTrailingSpace(t *testing.T) {
	predictor := NewBashPredictor(t.TempDir(), nil)
	items, err := predictor.Predict(t.Context(), "git")
	require.NoError(t, err)
	assert.Contains(t, suggestionCommands(items), "git ", "the space enters the subcommand position")
	assert.NotContains(t, suggestionCommands(items), "git")

	subcommands, err := predictor.Predict(t.Context(), "git ")
	require.NoError(t, err)
	require.NotEmpty(t, subcommands)
	assert.NotContains(t, suggestionCommands(subcommands), "git ")
	for _, item := range subcommands {
		assert.True(t, strings.HasPrefix(item.Command, "git "), item.Command)
	}
	assert.NotEqual(t, items, subcommands)
}

func TestPredictQuotedPathsUseInjectedDirectory(t *testing.T) {
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "my file.txt"), nil, 0o600))
	predictor := NewBashPredictor(cwd, nil)
	for _, tt := range []struct {
		query string
		want  string
	}{
		{`cat "my fi`, `cat "my file.txt"`},
		{`cat 'my fi`, `cat 'my file.txt'`},
		{`cat my\ fi`, `cat 'my file.txt'`},
	} {
		t.Run(tt.query, func(t *testing.T) {
			items, err := predictor.Predict(t.Context(), tt.query)
			require.NoError(t, err)
			require.Len(t, items, 1)
			assert.Equal(t, tt.want, items[0].Command)
			assert.Equal(t, strings.HasPrefix(tt.want, tt.query), items[0].IsPrefix)
			assert.Equal(t, "files", items[0].Description)
		})
	}

	items, err := predictor.Predict(t.Context(), `cat "my file.txt"`)
	require.NoError(t, err)
	assert.Empty(t, items, "an identical full-line completion is omitted")
}

func TestPredictFuzzyCompletion(t *testing.T) {
	items, err := NewBashPredictor(t.TempDir(), nil).Predict(t.Context(), "git chekout")
	require.NoError(t, err)
	require.NotEmpty(t, items)
	assert.Contains(t, suggestionCommands(items), "git checkout ")
	for _, item := range items {
		assert.False(t, item.IsPrefix, item.Command)
	}
}

func TestPredictBlankQueryDoesNotInitializeEngine(t *testing.T) {
	predictor := NewBashPredictor(t.TempDir(), nil)
	predictor.engine = func() (*sev.Engine, error) {
		assert.Fail(t, "blank query must not initialize the engine")
		return nil, errors.New("unexpected initialization")
	}
	for _, query := range []string{"", " \t "} {
		items, err := predictor.Predict(t.Context(), query)
		require.NoError(t, err)
		assert.Empty(t, items)
	}
}

func TestPredictCanceledContext(t *testing.T) {
	predictor := NewBashPredictor(t.TempDir(), nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	items, err := predictor.Predict(ctx, "git ch")
	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, items)

	items, err = predictor.Predict(t.Context(), "git ch")
	require.NoError(t, err)
	assert.NotEmpty(t, items, "cancellation must not poison later queries")
}

func TestPredictInitializationError(t *testing.T) {
	want := errors.New("cannot load completion specs")
	predictor := NewBashPredictor(t.TempDir(), nil)
	predictor.engine = sync.OnceValues(func() (*sev.Engine, error) { return nil, want })
	items, err := predictor.Predict(t.Context(), "git ch")
	assert.ErrorIs(t, err, want)
	assert.Empty(t, items)
}

func TestPredictOverlappingCalls(t *testing.T) {
	predictor := NewBashPredictor(t.TempDir(), nil)
	for i := range 12 {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			t.Parallel()
			query := "git ch"
			if i%2 == 0 {
				query = "git log --"
			}
			items, err := predictor.Predict(t.Context(), query)
			require.NoError(t, err)
			require.NotEmpty(t, items)
			for _, item := range items {
				assert.True(t, strings.HasPrefix(item.Command, query), item.Command)
			}
		})
	}
}

func TestPredictUsesInjectedHook(t *testing.T) {
	cwd := t.TempDir()
	predictor := NewBashPredictor(cwd, func(_ context.Context, req sev.Request) ([]sev.Item, bool, error) {
		assert.Equal(t, cwd, req.Cwd)
		assert.Equal(t, bashSuggestVisible, req.Limit)
		return []sev.Item{{Text: req.Line + "completed", Desc: "custom completion", Score: 1}}, true, nil
	})
	items, err := predictor.Predict(t.Context(), "custom ")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "custom completed", items[0].Command)
	assert.Equal(t, "custom completion", items[0].Description)
	assert.True(t, items[0].IsPrefix)
}

func suggestionCommands(items []controller.BashSuggestion) []string {
	commands := make([]string, len(items))
	for i, item := range items {
		commands[i] = item.Command
	}
	return commands
}
