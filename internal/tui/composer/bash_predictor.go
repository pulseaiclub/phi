package composer

import (
	"context"
	"strings"

	"github.com/pulseaiclub/phi/internal/session/shellhist"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// BashHistoryPredictor completes "!" text from the project shell history.
// shellhist owns reading and caching history.jsonl; this type is only the seam
// between that store and the composer's predictor interface.
type BashHistoryPredictor struct {
	store *shellhist.Store
}

// NewBashHistoryPredictor builds a predictor over a project history store. A nil
// store leaves the predictor inert: Predict then reports no completions instead
// of panicking, so a caller can install it unconditionally.
func NewBashHistoryPredictor(store *shellhist.Store) *BashHistoryPredictor {
	return &BashHistoryPredictor{store: store}
}

// Predict lists recent commands from history that literally start with the typed
// text, newest first. Completion is prefix-only, like a shell: there is no
// fuzzy ranking, so a command either completes the text or it does not. A
// missing or unreadable history is an empty one: the picker stays quiet instead
// of surfacing an error the user cannot act on.
func (p *BashHistoryPredictor) Predict(_ context.Context, typed string) ([]controller.BashSuggestion, error) {
	if p == nil || p.store == nil {
		return nil, nil
	}
	query := strings.TrimSpace(typed)
	if query == "" {
		return nil, nil
	}
	history, err := p.store.Commands()
	if err != nil {
		history = nil
	}
	matches := prefixMatches(query, history, bashSuggestVisible)
	if len(matches) == 0 {
		return nil, nil
	}
	items := make([]controller.BashSuggestion, 0, len(matches))
	for index, command := range matches {
		items = append(items, controller.BashSuggestion{
			Command:  command,
			Score:    recencyScore(index, len(matches)),
			IsPrefix: true,
		})
	}
	return items, nil
}

// prefixMatches returns the most recent limit distinct commands that start with
// query, newest first. The command equal to the typed text is excluded — there
// is nothing left to complete — and duplicates keep their most recent position,
// matching what HIST_IGNORE_ALL_DUPS would show.
func prefixMatches(query string, history []string, limit int) []string {
	if limit < 1 {
		return nil
	}
	seen := make(map[string]struct{}, min(limit, len(history)))
	matches := make([]string, 0, min(limit, len(history)))
	for index := len(history) - 1; index >= 0 && len(matches) < limit; index-- {
		command := history[index]
		if command == query {
			continue
		}
		if !strings.HasPrefix(command, query) {
			continue
		}
		if _, duplicate := seen[command]; duplicate {
			continue
		}
		seen[command] = struct{}{}
		matches = append(matches, command)
	}
	return matches
}

// recencyScore turns a 0-based newest-first index into a 0..1 weight: the most
// recent match scores 1, and each older one weighs less. The picker shows it so
// a fresher completion reads differently from a stale one.
func recencyScore(index, total int) float64 {
	if total <= 1 {
		return 1
	}
	return 1 - float64(index)/float64(total)
}
