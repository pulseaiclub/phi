package composer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pulseaiclub/phi/internal/bashcomplete"
	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chat"
	"github.com/pulseaiclub/phi/internal/components/mention"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

const (
	// bashSuggestDebounce waits for a pause in typing. Every source is a local
	// read with a cache behind it, so the pause only needs to coalesce a burst
	// of keystrokes — completion should feel like it tracks the typing.
	bashSuggestDebounce = 50 * time.Millisecond
	// bashSuggestTimeout bounds one read end to end, so a wedged request
	// cannot hold the picker open forever.
	bashSuggestTimeout = 10 * time.Second
	// bashSuggestVisible caps how many rows the picker lists.
	bashSuggestVisible = 10
)

// bashSuggest owns the "!" completer: the picker that draws matching commands,
// the completion in flight behind it, and the composer text that completion
// answers. ComposerPane holds one and delegates to it, so the state a completion
// source keeps lives with the completion source instead of as a handful of
// fields on the pane.
type bashSuggest struct {
	// pane is the composer this completer answers for, and the only place it reads
	// the text to rank, the bus to answer on, and the pickers to yield to from.
	// Some of those are wired after construction, so a copy taken here would go
	// stale: newBashSuggest sets it once, which also means a ComposerPane must not
	// be copied after it is built.
	pane   *ComposerPane
	picker mention.Picker
	// predictor ranks "!" completions. nil disables the picker entirely, so a
	// session without a predictor keeps the composer exactly as it was.
	predictor BashPredictor
	gen       int
	// query is the text the current rows (or the request in flight) answer,
	// so a cursor move reporting the same query is not answered twice.
	query string
	// accepted is the composer text an accept just produced. Filling the
	// composer fires a change, and the picker must not reopen over its own
	// answer and ask again.
	accepted string
	// cancel stops the prediction in flight; nil before the first one.
	cancel context.CancelFunc
	// rows holds the suggestion behind each picker item, index-aligned, so an
	// accept knows how the highlighted row replaces the composer text. Rows set
	// straight on the picker have none, and are whole-line rows.
	rows []controller.BashSuggestion
}

// row returns the suggestion behind the highlighted picker row, if one is
// aligned with it.
func (b *bashSuggest) row() (controller.BashSuggestion, bool) {
	if b.picker.Selected < 0 || b.picker.Selected >= len(b.rows) {
		return controller.BashSuggestion{}, false
	}
	return b.rows[b.picker.Selected], true
}

// newBashSuggest builds the "!" completer for one composer pane: it reads the
// text it ranks, and the bus it answers on, from there.
func newBashSuggest(pane *ComposerPane, theme components.Theme) bashSuggest {
	return bashSuggest{
		pane: pane,
		picker: mention.Picker{
			Theme:    theme,
			Prefix:   "!",
			MaxItems: bashSuggestVisible,
		},
	}
}

// SetBashPredictor installs the "!" completion source. Until one is set — or if
// none is ever set, on a build without a predictor — the picker stays closed and
// the composer behaves exactly as before.
func (c *ComposerPane) SetBashPredictor(predictor BashPredictor) {
	if c == nil {
		return
	}
	c.bash.predictor = predictor
}

// onChange reacts to the composer text entering or leaving "!" mode.
//
// Two rules keep the picker from getting in the way of running a command:
//   - it appears only when it has something to say, and owns the navigation keys
//     only while it has rows to navigate. Until a completion arrives, Enter must run
//     what the user typed rather than be swallowed by an empty list.
//   - filling the composer is an answer, not a new question, so the text just
//     accepted is not re-submitted.
func (b *bashSuggest) onChange(active bool, query string) {
	// The accepted suggestion is now the composer text: the change it fired is
	// not a new query.
	if b.accepted != "" && b.pane.Chat.Value == b.accepted {
		b.hide()
		return
	}
	b.accepted = ""
	if !active || b.predictor == nil {
		b.hide()
		return
	}
	if b.yields() {
		b.hide()
		return
	}
	if strings.TrimSpace(query) == "" {
		b.hide()
		return
	}
	// A cursor move reports the same query again. Answering it twice would redo
	// the work and flicker the rows away for nothing, so the matches already
	// in hand (or already in flight) are kept.
	if query == b.query && (len(b.picker.Items) > 0 || b.cancel != nil) {
		b.show()
		return
	}

	// A ranking is only meaningful for the text it was computed for, so rows
	// from the previous query are dropped rather than shown against new text:
	// a score list that silently belongs to other text is worse than a blank
	// list for the time it takes to replace it. The picker goes with the
	// rows — a list with nothing in it has nothing to say — and so does any
	// placeholder for them, however long that takes.
	b.rows = nil
	b.pane.Chat.GhostText = ""
	b.picker.SetResults(nil, "")
	b.show()
	b.schedule(query)
}

// ghostRemainder is the inline hint for the highlighted row: the rest of that
// row the user has not typed yet. A word row hints the rest of its word; a
// history row the rest of the line. Rows that do not literally extend what is
// typed have no inline form and hint nothing.
func (b *bashSuggest) ghostRemainder() string {
	row, ok := b.row()
	if !ok {
		return ""
	}
	query, _, ok := chat.ActiveBash(b.pane.Chat.Value, b.pane.Chat.Cursor)
	if !ok {
		return ""
	}
	if row.Insert != "" {
		words := bashcomplete.Tokenize(query)
		typed := words[len(words)-1].Text
		if strings.HasPrefix(row.Insert, typed) {
			return row.Insert[len(typed):]
		}
		return ""
	}
	if strings.HasPrefix(row.Command, query) {
		return row.Command[len(query):]
	}
	return ""
}

// yields reports whether another completer owns the composer: `@path` can sit
// inside a "!" command, and two pickers must not fight over one keystroke. The
// newest completer wins, as it does between mention and slash.
func (b *bashSuggest) yields() bool {
	c := b.pane
	return c.slash.Open || c.Chat.SlashOpen || c.question.Open || c.Chat.QuestionOpen ||
		c.mention.Open || c.Chat.MentionOpen
}

// show reveals the picker when it has something to say — rows to walk
// or a status to read — and claims the navigation keys only with rows: an
// open-but-empty list that ate Enter would make running a "!" command need two.
// Visibility is derived from the content here, so no caller has to order its own
// mutations against it.
func (b *bashSuggest) show() {
	if len(b.picker.Items) == 0 && b.picker.Status == "" {
		b.picker.Hide()
		b.pane.Chat.BashOpen = false
		return
	}
	b.picker.Show()
	b.pane.Chat.BashOpen = len(b.picker.Items) > 0
}

// rowIsTypedText reports whether the highlighted row is already what the
// user typed. Accepting it would rewrite the composer with itself, so Enter
// keeps its usual meaning — run the command. A word row is compared against
// the word being typed; a whole-line row against the whole query.
func (b *bashSuggest) rowIsTypedText() bool {
	if b.picker.Selected < 0 || b.picker.Selected >= len(b.picker.Items) {
		return false
	}
	query, _, ok := chat.ActiveBash(b.pane.Chat.Value, b.pane.Chat.Cursor)
	if !ok {
		return false
	}
	if row, ok := b.row(); ok && row.Insert != "" {
		word := bashcomplete.Tokenize(query)
		return strings.TrimSpace(row.Insert) == strings.TrimSpace(word[len(word)-1].Text)
	}
	return strings.TrimSpace(b.picker.Items[b.picker.Selected].Path) == strings.TrimSpace(query)
}

// pending reports whether there is anything of the "!" completer left to
// dismiss: rows on screen, or a completion in flight that has not drawn yet.
func (b *bashSuggest) pending() bool {
	return b.picker.Open || b.cancel != nil
}

// hide closes the picker and stops the prediction behind it.
// The query is forgotten with it: re-entering "!" mode is a fresh question, and
// nothing is left behind to answer it with.
func (b *bashSuggest) hide() {
	b.rows = nil
	b.pane.Chat.GhostText = ""
	b.picker.SetResults(nil, "")
	b.picker.Hide()
	b.pane.Chat.BashOpen = false
	b.query = ""
	b.abandon()
}

// abandon drops any prediction still in flight. Bumping the
// generation alone only makes the UI ignore the answer; the request keeps
// running until its context is cancelled.
func (b *bashSuggest) abandon() {
	b.gen++
	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
}

// schedule debounces one keystroke's query, then asks the predictor.
// The work runs off the UI goroutine and answers on the bus, like the @-file
// search: the composer never blocks on a history read.
//
// Nothing is drawn while it waits. A row saying "asking" would appear a beat
// before the ranking replaces it, which reads as a flicker rather than as
// information, and the wait is bounded by bashSuggestTimeout either way.
func (b *bashSuggest) schedule(query string) {
	if b.predictor == nil {
		return
	}
	b.abandon()
	gen := b.gen
	predictor := b.predictor
	bus := b.pane.bus
	// Recorded here rather than by the caller, so abandoning the request above
	// cannot erase what this one was asked about.
	b.query = query

	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel

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
	if c == nil {
		return
	}
	c.bash.apply(msg)
}

// apply applies one prediction.
func (b *bashSuggest) apply(msg controller.BashSuggestionsMsg) {
	if msg.Gen != b.gen {
		return
	}
	// The buffer may have moved on without a new prediction being scheduled
	// (a cursor move, say): showing this ranking would suggest the wrong
	// completion for what is on screen.
	if query, _, ok := chat.ActiveBash(b.pane.Chat.Value, b.pane.Chat.Cursor); !ok || query != msg.Query {
		return
	}
	if msg.ErrText != "" {
		// The picker stays up to say why, but with no rows it must not hold the
		// navigation keys: Enter still has to run what the user typed.
		b.picker.SetResults(nil, msg.ErrText)
		b.show()
		return
	}
	if len(msg.Items) == 0 {
		// Nothing worth showing: a picker with no rows is noise.
		b.hide()
		return
	}
	b.rows = msg.Items
	items := make([]mention.Item, 0, len(msg.Items))
	for _, item := range msg.Items {
		items = append(items, mention.Item{
			Path:        item.Command,
			Description: bashSuggestionLabel(item),
		})
	}
	b.picker.SetResults(items, "")
	// There are rows to navigate now, so the picker opens and takes the
	// navigation keys back from the composer.
	b.show()
	// The top row doubles as the inline hint, so the picker and the ghost
	// never disagree about what Tab would accept.
	b.pane.Chat.GhostText = b.ghostRemainder()
}

// bashSuggestionLabel names where the row came from, so a literal history
// match reads differently from a word nobody typed yet. A history row between
// matches also shows its recency weight.
func bashSuggestionLabel(item controller.BashSuggestion) string {
	switch item.Source {
	case string(bashcomplete.SourceCommand),
		string(bashcomplete.SourceArgument),
		string(bashcomplete.SourceCommon),
		string(bashcomplete.SourcePath):
		return item.Source
	}
	if item.Score > 0 && item.Score < 1 {
		return fmt.Sprintf("%s · %.2f", bashcomplete.SourceHistory, item.Score)
	}
	return string(bashcomplete.SourceHistory)
}

// accept fills the composer with the chosen completion, replacing what was
// typed: a whole command for history rows, the word being typed for word rows.
func (b *bashSuggest) accept(item mention.Item) {
	start, end, text := b.replacement(item)
	b.hide()
	b.pane.Chat.ReplaceRange(start, end, text)
	// ReplaceRange notifies synchronously, so the picker may already have
	// reopened for the text just accepted. Record that this text is an answer
	// already given, and close what that notification opened.
	b.accepted = b.pane.Chat.Value
	b.hide()
	if b.pane.onRedraw != nil {
		b.pane.onRedraw()
	}
}

// replacement resolves what accepting the highlighted row does to the composer
// text. A whole-line row carries a full command and replaces the whole command
// text; a word row carries the word to insert and replaces only the word being
// typed, because the rest of the line is already right.
func (b *bashSuggest) replacement(item mention.Item) (start, end int, text string) {
	if row, ok := b.row(); ok && row.Insert != "" {
		if query, qstart, ok := chat.ActiveBash(b.pane.Chat.Value, b.pane.Chat.Cursor); ok {
			words := bashcomplete.Tokenize(query)
			word := words[len(words)-1]
			return qstart + word.Start, b.pane.Chat.Cursor, row.Insert
		}
	}
	start, end = b.replaceRange()
	return start, end, item.Path
}

// replaceRange resolves the composer range an accepted suggestion replaces:
// the whole command text after "!", or the cursor when "!" mode is somehow no
// longer active.
//
// The range runs to the end of the buffer, not to the cursor: a row is a whole
// command, so replacing only up to the cursor would splice the accepted command
// into the middle of the typed one.
func (b *bashSuggest) replaceRange() (start, end int) {
	if _, start, ok := chat.ActiveBash(b.pane.Chat.Value, b.pane.Chat.Cursor); ok {
		return start, len(b.pane.Chat.Value)
	}
	return b.pane.Chat.Cursor, b.pane.Chat.Cursor
}

// bashSuggestError turns a prediction failure into something the user can act
// on, rather than a transport-level string.
func bashSuggestError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "Completions took too long — keep typing or try again"
	default:
		return "Completions unavailable"
	}
}
