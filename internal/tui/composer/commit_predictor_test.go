package composer

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/tui/controller"
)

type countingBashPredictor struct{ calls int }

func (p *countingBashPredictor) Predict(context.Context, string) ([]controller.BashSuggestion, error) {
	p.calls++
	return []controller.BashSuggestion{{Command: "git checkout "}}, nil
}

func TestCommitPredictorRouting(t *testing.T) {
	failure := errors.New("model unavailable")
	for _, tt := range []struct {
		name     string
		command  string
		handled  bool
		err      error
		want     string
		fallback int
	}{
		{name: "ordinary command", want: "git checkout ", fallback: 1},
		{name: "message", command: `git commit -m "fix: handle expiry"`, handled: true, want: `git commit -m "fix: handle expiry"`},
		{name: "empty index", handled: true},
		{name: "failure", handled: true, err: failure},
		{name: "rewritten prefix", command: `git commit -am "fix: handle expiry"`, handled: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fallback := &countingBashPredictor{}
			p := NewCommitBashPredictor(fallback, func(context.Context, string) (string, bool, error) {
				return tt.command, tt.handled, tt.err
			})
			items, err := p.Predict(t.Context(), `git commit -m "`)
			assert.ErrorIs(t, err, tt.err)
			assert.Equal(t, tt.fallback, fallback.calls)
			if tt.want == "" {
				assert.Empty(t, items)
				return
			}
			require.Len(t, items, 1)
			assert.Equal(t, tt.want, items[0].Command)
			if tt.handled {
				assert.True(t, items[0].IsPrefix)
				assert.Equal(t, "AI commit message", items[0].Description)
			}
		})
	}
}

func TestCommitPredictorDropsCanceledResult(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p := NewCommitBashPredictor(&countingBashPredictor{}, func(context.Context, string) (string, bool, error) {
		cancel()
		return `git commit -m "fix: expiry"`, true, nil
	})
	items, err := p.Predict(ctx, `git commit -m "`)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, items)
}
