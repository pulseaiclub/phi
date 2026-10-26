package composer

import (
	"context"

	"github.com/pulseaiclub/phi/internal/bashcomplete"
	"github.com/pulseaiclub/phi/internal/session/shellhist"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// LocalBashPredictor completes "!" text from local sources: the project shell
// history, executables on PATH, and the project directory. bashcomplete owns
// choosing and ranking candidates; this type is only the seam between it, the
// history store, and the composer's predictor interface.
type LocalBashPredictor struct {
	completer *bashcomplete.Completer
	// commands overrides the PATH source, so tests complete deterministically
	// without depending on the machine's PATH.
	commands func() []string
}

// NewLocalBashPredictor builds the "!" completion source over a project
// history store and directory. A nil store leaves the history sources empty:
// PATH and path completion still work, so a project without a session keeps
// working completions instead of a predictor that errors.
func NewLocalBashPredictor(store *shellhist.Store, cwd string) *LocalBashPredictor {
	predictor := &LocalBashPredictor{}
	var history func() []string
	if store != nil {
		history = func() []string {
			// An unreadable history is an empty one: completions degrade to
			// the sources that are readable instead of erroring on a keystroke.
			commands, err := store.Commands()
			if err != nil {
				return nil
			}
			return commands
		}
	}
	predictor.completer = bashcomplete.New(history, predictor.pathCommands, cwd)
	return predictor
}

// pathCommands feeds the completer its executable names.
func (p *LocalBashPredictor) pathCommands() []string {
	if p.commands != nil {
		return p.commands()
	}
	return bashcomplete.PathCommands()
}

// Predict lists completions for typed, most literal first. Every source is a
// local read, so nothing here can fail in a way the user could act on.
func (p *LocalBashPredictor) Predict(_ context.Context, typed string) ([]controller.BashSuggestion, error) {
	if p == nil || p.completer == nil {
		return nil, nil
	}
	// Every row goes to the picker: it shows a scrolling window, and cutting
	// the list here would decide for the user which candidates exist.
	rows := p.completer.Complete(typed)
	items := make([]controller.BashSuggestion, 0, len(rows))
	for _, row := range rows {
		items = append(items, controller.BashSuggestion{
			Command:     row.Command,
			Insert:      row.Insert,
			Source:      string(row.Source),
			Score:       row.Score,
			Description: row.Description,
		})
	}
	return items, nil
}
