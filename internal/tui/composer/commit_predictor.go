package composer

import (
	"context"
	"strings"

	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// CommitBashPredictor delegates message-entry queries to an injected completer.
// Everything else keeps the ordinary shell predictor's latency and behavior.
type CommitBashPredictor struct {
	fallback BashPredictor
	complete func(context.Context, string) (string, bool, error)
}

func NewCommitBashPredictor(
	fallback BashPredictor,
	complete func(context.Context, string) (string, bool, error),
) *CommitBashPredictor {
	return &CommitBashPredictor{fallback: fallback, complete: complete}
}

func (p *CommitBashPredictor) Predict(ctx context.Context, typed string) ([]controller.BashSuggestion, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	command, handled, err := p.complete(ctx, typed)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !handled {
		return p.fallback.Predict(ctx, typed)
	}
	if command == "" || command == typed || !strings.HasPrefix(command, typed) {
		return nil, nil
	}
	return []controller.BashSuggestion{{
		Command:     command,
		Description: "AI commit message",
		Score:       100,
		IsPrefix:    true,
	}}, nil
}
