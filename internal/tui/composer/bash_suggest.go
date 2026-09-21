package composer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pulseaiclub/phi/internal/components/chat"
	"github.com/pulseaiclub/phi/internal/components/mention"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

const (
	// bashSuggestDebounce waits for a pause in typing. A judgement costs a round
	// trip, so firing one per keystroke would spend the budget on prefixes the
	// user never meant to ask about.
	bashSuggestDebounce = 200 * time.Millisecond
	// bashSuggestTimeout bounds one judgement end to end, so a wedged request
	// cannot hold the picker open forever.
	bashSuggestTimeout = 10 * time.Second
	// bashSuggestVisible caps how many rows the picker lists.
	bashSuggestVisible = 10
)

// SetBashPredictor installs the "!" completion source. Until one is set — or if
// none is ever set, on a build without a judge — the picker stays closed and
// the composer behaves exactly as before.
func (c *ComposerPane) SetBashPredictor(predictor BashPredictor) {
	if c == nil {
		return
	}
	c.bashPredict = predictor
}

// onBashChange reacts to the composer text entering or leaving "!" mode.
//
// Two rules keep the picker from getting in the way of running a command:
//   - it owns the navigation keys only while it has rows to navigate. Until a
//     ranking arrives, Enter must run what the user typed rather than be
//     swallowed by an empty list.
//   - filling the composer is an answer, not a new question, so the text just
//     accepted is not re-submitted for judgement.
func (c *ComposerPane) onBashChange(active bool, query string) {
	if c == nil {
		return
	}
	// The accepted suggestion is now the composer text: the change it fired is
	// not a new query.
	if c.bashAccepted != "" && c.Chat.Value == c.bashAccepted {
		c.hideBashSuggestions()
		return
	}
	c.bashAccepted = ""
	if !active || c.bashPredict == nil {
		c.hideBashSuggestions()
		return
	}
	// Another completer owns the composer while it is open: `@path` can sit
	// inside a "!" command, and two pickers must not fight over one keystroke.
	// The newest completer wins, as it does between mention and slash.
	if c.slash.Open || c.Chat.SlashOpen || c.question.Open || c.Chat.QuestionOpen ||
		c.mention.Open || c.Chat.MentionOpen {
		c.hideBashSuggestions()
		return
	}
	if strings.TrimSpace(query) == "" {
		c.hideBashSuggestions()
		return
	}
	// A cursor move reports the same query again. Answering it twice would burn
	// a round trip and flicker the rows away for nothing, so the ranking already
	// in hand (or already in flight) is kept.
	if query == c.bashQuery && (len(c.bash.Items) > 0 || c.bashCancel != nil) {
		c.showBashPicker()
		return
	}

	c.showBashPicker()
	// A ranking is only meaningful for the text it was computed for, so rows
	// from the previous query are dropped rather than shown against new text:
	// a score list that silently belongs to other text is worse than a blank
	// list for the round trip it takes to replace it.
	c.bash.Items = nil
	c.bash.Status = "Asking Jev…"
	c.scheduleBashSuggest(query)
}

// showBashPicker reveals the picker for the current query. It claims the
// navigation keys only once there are rows: an open-but-empty list that ate
// Enter would make running a "!" command need two of them.
func (c *ComposerPane) showBashPicker() {
	c.bash.Show()
	c.Chat.BashOpen = len(c.bash.Items) > 0
}

// bashRowIsTypedText reports whether the highlighted row is already what the
// user typed. Accepting it would rewrite the composer with itself, so Enter
// keeps its usual meaning — run the command.
func (c *ComposerPane) bashRowIsTypedText() bool {
	if c.bash.Selected < 0 || c.bash.Selected >= len(c.bash.Items) {
		return false
	}
	query, _, _, ok := chat.ActiveBash(c.Chat.Value, c.Chat.Cursor)
	if !ok {
		return false
	}
	return strings.TrimSpace(c.bash.Items[c.bash.Selected].Path) == strings.TrimSpace(query)
}

// hideBashSuggestions closes the picker and stops the prediction behind it.
// The query is forgotten with it: re-entering "!" mode is a fresh question.
func (c *ComposerPane) hideBashSuggestions() {
	if c == nil {
		return
	}
	c.bash.Hide()
	c.Chat.BashOpen = false
	c.bashQuery = ""
	c.abandonBashSuggest()
}

// abandonBashSuggest drops any prediction still in flight. Bumping the
// generation alone only makes the UI ignore the answer; the request keeps
// burning a round trip until its context is cancelled.
func (c *ComposerPane) abandonBashSuggest() {
	c.bashGen++
	if c.bashCancel != nil {
		c.bashCancel()
		c.bashCancel = nil
	}
}

// scheduleBashSuggest debounces one keystroke's query, then asks the predictor.
// The work runs off the UI goroutine and answers on the bus, like the @-file
// search: the composer never blocks on a judgement.
func (c *ComposerPane) scheduleBashSuggest(query string) {
	if c == nil || c.bashPredict == nil {
		return
	}
	c.abandonBashSuggest()
	gen := c.bashGen
	predictor := c.bashPredict
	bus := c.bus
	// Recorded here rather than by the caller, so abandoning the request above
	// cannot erase what this one was asked about.
	c.bashQuery = query

	ctx, cancel := context.WithCancel(context.Background())
	c.bashCancel = cancel

	go func() {
		defer cancel()
		select {
		case <-time.After(bashSuggestDebounce):
		case <-ctx.Done():
			return
		}

		predictCtx, predictCancel := context.WithTimeout(ctx, bashSuggestTimeout)
		defer predictCancel()
		items, err := predictor.Predict(predictCtx, query)

		// A superseded prediction has nothing useful to report.
		if ctx.Err() != nil {
			return
		}
		msg := controller.BashSuggestionsMsg{Gen: gen, Query: query, Items: items}
		if err != nil {
			msg.ErrText = bashSuggestError(err)
		}
		if bus != nil {
			bus.Publish(msg)
		}
	}()
}

// ApplyBashSuggestions applies one prediction on the UI goroutine.
func (c *ComposerPane) ApplyBashSuggestions(msg controller.BashSuggestionsMsg) {
	if c == nil || msg.Gen != c.bashGen || !c.bash.Open {
		return
	}
	// The buffer may have moved on without a new prediction being scheduled
	// (a cursor move, say): showing this ranking would suggest the wrong
	// completion for what is on screen.
	if query, _, _, ok := chat.ActiveBash(c.Chat.Value, c.Chat.Cursor); !ok || query != msg.Query {
		return
	}
	if msg.ErrText != "" {
		c.bash.SetResults(nil, msg.ErrText)
		return
	}
	if len(msg.Items) == 0 {
		// Nothing worth showing: a picker with no rows is noise.
		c.hideBashSuggestions()
		return
	}
	items := make([]mention.Item, 0, len(msg.Items))
	for _, item := range msg.Items {
		items = append(items, mention.Item{
			Path:        item.Command,
			Description: bashSuggestionLabel(item),
		})
	}
	c.bash.SetResults(items, "")
	// There are rows to navigate now, so the picker takes the navigation keys
	// back from the composer.
	c.Chat.BashOpen = true
}

// bashSuggestionLabel describes how the row was ranked, so a literal completion
// reads differently from a guess.
func bashSuggestionLabel(item controller.BashSuggestion) string {
	if item.IsPrefix {
		return fmt.Sprintf("%.2f · prefix", item.Score)
	}
	return fmt.Sprintf("%.2f", item.Score)
}

// acceptBash fills the composer with the chosen command, replacing what was
// typed. The command is inserted whole because the picker ranks whole commands:
// the typed text may be an abbreviation rather than a prefix.
func (c *ComposerPane) acceptBash(item mention.Item) {
	if c == nil {
		return
	}
	start, end := c.bashReplaceRange()
	c.hideBashSuggestions()
	c.Chat.ReplaceRange(start, end, item.Path)
	// ReplaceRange notifies synchronously, so the picker may already have
	// reopened for the text just accepted. Record that this text is an answer
	// already given, and close what that notification opened.
	c.bashAccepted = c.Chat.Value
	c.hideBashSuggestions()
	if c.onRedraw != nil {
		c.onRedraw()
	}
}

// bashReplaceRange resolves the composer range an accepted suggestion replaces:
// the whole command text after "!", or the cursor when "!" mode is somehow no
// longer active.
//
// The range runs to the end of the buffer, not to the cursor: a row is a whole
// command, so replacing only up to the cursor would splice the accepted command
// into the middle of the typed one.
func (c *ComposerPane) bashReplaceRange() (start, end int) {
	if _, start, _, ok := chat.ActiveBash(c.Chat.Value, c.Chat.Cursor); ok {
		return start, len(c.Chat.Value)
	}
	return c.Chat.Cursor, c.Chat.Cursor
}

// bashSuggestError turns a prediction failure into something the user can act
// on, rather than a transport-level string.
func bashSuggestError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "Jev took too long — keep typing or try again"
	default:
		return "Completions unavailable"
	}
}
