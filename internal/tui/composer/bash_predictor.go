package composer

import (
	"context"
	"strings"
	"sync"

	"github.com/pulseaiclub/sev"

	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// SevBashPredictor completes "!" text using command specs and dynamic arguments.
// The working directory is fixed when the composer is constructed.
type SevBashPredictor struct {
	cwd    string
	engine func() (*sev.Engine, error)
}

// NewBashPredictor defers loading command specs until the first query, so engine
// initialization errors travel through Predict without blocking editor startup.
func NewBashPredictor(cwd string, hook sev.CompletionHook) *SevBashPredictor {
	return &SevBashPredictor{
		cwd: cwd,
		engine: sync.OnceValues(func() (*sev.Engine, error) {
			return sev.New(sev.Options{
				Limit: bashSuggestVisible, DisableHistory: true, CompletionHook: hook,
			})
		}),
	}
}

// Predict preserves shell syntax in both the query and the engine's full-line
// completions. Only an identical result has nothing left to complete.
func (p *SevBashPredictor) Predict(ctx context.Context, typed string) ([]controller.BashSuggestion, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(typed) == "" {
		return nil, nil
	}
	engine, err := p.engine()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	matches, err := engine.Complete(ctx, sev.Request{Line: typed, Cwd: p.cwd})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items := make([]controller.BashSuggestion, 0, len(matches))
	for _, item := range matches {
		if item.Text == typed {
			continue
		}
		items = append(items, controller.BashSuggestion{
			Command:     item.Text,
			Description: item.Desc,
			Score:       item.Score,
			IsPrefix:    strings.HasPrefix(item.Text, typed),
		})
	}
	return items, nil
}
