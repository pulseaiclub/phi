package composer

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/mention"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// fakeBashPredictor records the text each prediction was asked about and
// returns whatever the test wired up, so tests assert on the composer's
// behavior rather than on a live judge.
type fakeBashPredictor struct {
	mu    sync.Mutex
	typed []string
	items []controller.BashSuggestion
	err   error
	// waitForCancel keeps the prediction in flight until its context is cancelled.
	waitForCancel bool
}

func (f *fakeBashPredictor) Predict(ctx context.Context, typed string) ([]controller.BashSuggestion, error) {
	f.mu.Lock()
	f.typed = append(f.typed, typed)
	items, err := f.items, f.err
	f.mu.Unlock()

	if err != nil {
		return nil, err
	}
	// Hold the call until the context ends, so a cancel is observable.
	if f.waitForCancel {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return items, nil
}

func (f *fakeBashPredictor) asked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.typed...)
}

func newBashPane(t *testing.T, predictor BashPredictor) (*ComposerPane, *controller.Bus) {
	t.Helper()
	c := NewComposerPane(components.DefaultTheme(), "m", t.TempDir())
	bus := controller.NewBus(nil)
	c.bus = bus
	c.SetBashPredictor(predictor)
	return c, bus
}

// drainBash waits for one prediction to land on the bus and returns it.
func drainBash(t *testing.T, bus *controller.Bus) controller.BashSuggestionsMsg {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range bus.Drain() {
			if msg, ok := m.(controller.BashSuggestionsMsg); ok {
				return msg
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.Fail(t, "no bash suggestions arrived")
	return controller.BashSuggestionsMsg{}
}

func TestBashSuggestionsRankIntoThePicker(t *testing.T) {
	predictor := &fakeBashPredictor{items: []controller.BashSuggestion{
		{Command: "git stash", Score: 0.7},
		{Command: "git status", Score: 0.3, IsPrefix: true},
	}}
	c, bus := newBashPane(t, predictor)

	c.Chat.Value, c.Chat.Cursor = "!git s", len("!git s")
	c.bash.onChange(true, "git s")
	assert.False(t, c.bash.picker.Open, "typing in ! mode shows nothing: there is nothing to show yet")
	assert.False(t, c.Chat.BashOpen, "an empty list must not own the navigation keys")
	assert.Empty(t, c.bash.picker.Status, "and no status stands in for the ranking it is waiting for")

	c.ApplyBashSuggestions(drainBash(t, bus))

	assert.Equal(t, []mention.Item{
		{Path: "git stash", Description: "0.70"},
		{Path: "git status", Description: "0.30 · prefix"},
	}, c.bash.picker.Items)
	assert.Empty(t, c.bash.picker.Status, "results leave no status behind")
	assert.True(t, c.bash.picker.Open, "the ranking opens the picker")
	assert.True(t, c.Chat.BashOpen, "rows to navigate means the picker owns the keys")
	assert.Equal(t, []string{"git s"}, predictor.asked())
}

func TestBashCompletionSpaceCanBeAccepted(t *testing.T) {
	pane, _ := newBashPane(t, nil)
	pane.Chat.Value, pane.Chat.Cursor = "!git", len("!git")
	pane.ApplyBashSuggestions(controller.BashSuggestionsMsg{
		Query: "git",
		Items: []controller.BashSuggestion{{Command: "git ", Description: "Version control", IsPrefix: true}},
	})
	require.Len(t, pane.bash.picker.Items, 1)
	assert.Equal(t, "Version control", pane.bash.picker.Items[0].Description)
	assert.False(t, pane.bash.rowIsTypedText(), "a completion space changes the shell argument position")
	pane.bash.accept(pane.bash.picker.Items[0])
	assert.Equal(t, "!git ", pane.Chat.Value)
}

func TestAcceptBashReplacesTheTypedCommand(t *testing.T) {
	// Wired like the app: the accept path runs through ReplaceRange, whose
	// change notification is exactly what must not reopen the picker.
	c, bus := wiredComposer(t)
	c.SetBashPredictor(&fakeBashPredictor{})
	c.Chat.Value, c.Chat.Cursor = "!git s", len("!git s")
	c.bash.onChange(true, "git s")
	c.bash.picker.SetResults([]mention.Item{{Path: "git status", Description: "0.90"}}, "")
	c.bash.show()

	c.bash.accept(c.bash.picker.Items[0])

	assert.Equal(t, "!git status", c.Chat.Value, "the whole command replaces what was typed")
	assert.Equal(t, len("!git status"), c.Chat.Cursor)
	assert.False(t, c.Chat.BashOpen, "accepting closes the picker")
	assert.False(t, c.bash.picker.Open, "filling the composer is an answer, not a new query")
	assert.Empty(t, bus.Drain(), "an accepted command is not re-submitted")
}

func TestAcceptBashKeepsTheBang(t *testing.T) {
	c, _ := newBashPane(t, &fakeBashPredictor{})
	c.Chat.Value, c.Chat.Cursor = "  !ls", len("  !ls")

	c.bash.accept(mention.Item{Path: "ls -la"})

	assert.Equal(t, "  !ls -la", c.Chat.Value, "the prefix before the bang is left alone")
}

// A row is a whole command, so accepting one replaces the whole command text —
// not just the part before the cursor, which would splice the accepted command
// into the middle of the typed one.
func TestAcceptBashReplacesTheWholeCommandFromAMidCommandCursor(t *testing.T) {
	c, _ := newBashPane(t, &fakeBashPredictor{})
	c.Chat.Value, c.Chat.Cursor = "!git status", 3

	c.bash.accept(mention.Item{Path: "git stash"})

	assert.Equal(t, "!git stash", c.Chat.Value)
}

func TestSupersededBashSuggestionStaysQuiet(t *testing.T) {
	predictor := &fakeBashPredictor{}
	c, bus := newBashPane(t, predictor)

	// The second keystroke lands inside the debounce window of the first.
	c.Chat.Value, c.Chat.Cursor = "!git s", len("!git s")
	c.bash.onChange(true, "git s")
	c.bash.onChange(true, "git st")

	first := drainBash(t, bus)
	assert.Equal(t, "git st", first.Query, "only the newest query is answered")
	assert.Equal(t, []string{"git st"}, predictor.asked(), "a superseded query never reaches the judge")
}

func TestNewQueryDropsThePreviousRanking(t *testing.T) {
	predictor := &fakeBashPredictor{items: []controller.BashSuggestion{{Command: "git stash", Score: 0.9}}}
	c, bus := newBashPane(t, predictor)

	c.Chat.Value, c.Chat.Cursor = "!git s", len("!git s")
	c.bash.onChange(true, "git s")
	c.ApplyBashSuggestions(drainBash(t, bus))
	require.Len(t, c.bash.picker.Items, 1)

	// Typing on: the shown scores belong to the text that produced them, so the
	// picker must not keep displaying them against the new query.
	c.Chat.Value, c.Chat.Cursor = "!git sta", len("!git sta")
	c.bash.onChange(true, "git sta")

	assert.Empty(t, c.bash.picker.Items, "a ranking for other text must not be shown")
	assert.Empty(t, c.bash.picker.Status, "and no placeholder stands in for it")
	assert.False(t, c.bash.picker.Open, "the dropped rows take the picker with them")
	assert.False(t, c.Chat.BashOpen, "the dropped rows give the keys back while the rerank runs")
}

func TestBashSuggestionsAreDroppedWhenTheRequestWasCancelled(t *testing.T) {
	predictor := &fakeBashPredictor{waitForCancel: true}
	c, bus := newBashPane(t, predictor)

	c.bash.onChange(true, "git s")
	waitFor(t, func() bool { return len(predictor.asked()) == 1 })
	c.bash.hide()

	time.Sleep(2 * bashSuggestDebounce)
	for _, m := range bus.Drain() {
		_, ok := m.(controller.BashSuggestionsMsg)
		assert.False(t, ok, "a cancelled prediction must not publish")
	}
}

func TestApplyBashSuggestionsIgnoresStaleResults(t *testing.T) {
	c, _ := newBashPane(t, &fakeBashPredictor{})
	c.Chat.Value, c.Chat.Cursor = "!git s", len("!git s")
	c.bash.onChange(true, "git s")

	c.ApplyBashSuggestions(controller.BashSuggestionsMsg{
		Gen:   c.bash.gen - 1,
		Query: "git s",
		Items: []controller.BashSuggestion{{Command: "git stash"}},
	})

	assert.Empty(t, c.bash.picker.Items, "a result from a superseded request is ignored")
}

func TestApplyBashSuggestionsIgnoresResultsForOldText(t *testing.T) {
	c, _ := newBashPane(t, &fakeBashPredictor{})
	c.Chat.Value, c.Chat.Cursor = "!git status", len("!git status")
	c.bash.onChange(true, "git status")

	// Same generation, but the composer has moved on since the request started.
	c.ApplyBashSuggestions(controller.BashSuggestionsMsg{
		Gen:   c.bash.gen,
		Query: "git s",
		Items: []controller.BashSuggestion{{Command: "git stash"}},
	})

	assert.Empty(t, c.bash.picker.Items, "a ranking for other text must not be shown")
}

func TestApplyBashSuggestionsHidesAnEmptyResult(t *testing.T) {
	c, _ := newBashPane(t, &fakeBashPredictor{})
	c.Chat.Value, c.Chat.Cursor = "!zzz", len("!zzz")
	c.bash.onChange(true, "zzz")

	c.ApplyBashSuggestions(controller.BashSuggestionsMsg{Gen: c.bash.gen, Query: "zzz"})

	assert.False(t, c.bash.picker.Open, "an empty picker is noise")
	assert.False(t, c.Chat.BashOpen)
}

func TestApplyBashSuggestionsReportsFailureWithoutRows(t *testing.T) {
	c, _ := newBashPane(t, &fakeBashPredictor{})
	c.Chat.Value, c.Chat.Cursor = "!git s", len("!git s")
	c.bash.onChange(true, "git s")

	c.ApplyBashSuggestions(controller.BashSuggestionsMsg{
		Gen:     c.bash.gen,
		Query:   "git s",
		ErrText: "Completions unavailable",
	})

	assert.Empty(t, c.bash.picker.Items)
	assert.Equal(t, "Completions unavailable", c.bash.picker.Status)
	assert.True(t, c.bash.picker.Open, "the picker opens to say why")
}

func TestBashPredictorFailureIsActionable(t *testing.T) {
	c, bus := newBashPane(t, &fakeBashPredictor{err: errors.New("history read failed (500): boom")})

	c.Chat.Value, c.Chat.Cursor = "!git s", len("!git s")
	c.bash.onChange(true, "git s")

	msg := drainBash(t, bus)

	assert.NotContains(t, msg.ErrText, "500", "a raw error is not something to show the user")
	assert.NotEmpty(t, msg.ErrText)
}

func TestBashSuggestionsStayOffWithoutAPredictor(t *testing.T) {
	c, bus := newBashPane(t, nil)

	c.bash.onChange(true, "git s")

	assert.False(t, c.bash.picker.Open)
	assert.False(t, c.Chat.BashOpen)
	assert.Empty(t, bus.Drain(), "no predictor, no request")
}

func TestBashSuggestionsSkipShortText(t *testing.T) {
	predictor := &fakeBashPredictor{}
	c, bus := newBashPane(t, predictor)

	c.Chat.Value, c.Chat.Cursor = "!", 1
	c.bash.onChange(true, "")
	c.Chat.Value = "!  "
	c.bash.onChange(true, "  ")

	assert.False(t, c.bash.picker.Open, "a bare bang is not a query")
	assert.Empty(t, predictor.asked())
	assert.Empty(t, bus.Drain())
}

func TestBashSuggestionsYieldToAnotherCompleter(t *testing.T) {
	predictor := &fakeBashPredictor{}
	c, _ := newBashPane(t, predictor)

	c.slash.Open, c.Chat.SlashOpen = true, true
	c.bash.onChange(true, "git s")

	assert.False(t, c.bash.picker.Open, "two pickers must not fight over one keystroke")
	assert.Empty(t, predictor.asked())

	c.slash.Open, c.Chat.SlashOpen = false, false
	c.mention.Open, c.Chat.MentionOpen = true, true
	c.bash.onChange(true, "git s")
	assert.Empty(t, predictor.asked())
}

func TestLeavingBashModeClosesThePicker(t *testing.T) {
	c, _ := newBashPane(t, &fakeBashPredictor{})
	c.Chat.Value, c.Chat.Cursor = "!git s", len("!git s")
	c.bash.onChange(true, "git s")
	c.bash.picker.SetResults([]mention.Item{{Path: "git stash"}}, "")
	c.bash.show()
	require.True(t, c.bash.picker.Open)

	c.bash.onChange(false, "")

	assert.False(t, c.bash.picker.Open)
	assert.False(t, c.Chat.BashOpen)
}

func TestHideCompletersCancelsThePrediction(t *testing.T) {
	predictor := &fakeBashPredictor{waitForCancel: true}
	c, _ := newBashPane(t, predictor)

	c.bash.onChange(true, "git s")
	waitFor(t, func() bool { return len(predictor.asked()) == 1 })

	cancelled := make(chan struct{})
	inner := c.bash.cancel
	require.NotNil(t, inner, "a prediction in flight must be cancellable")
	c.bash.cancel = func() { close(cancelled); inner() }

	c.HideCompleters()

	select {
	case <-cancelled:
	case <-time.After(time.Second):
		require.Fail(t, "closing the picker must cancel the prediction in flight")
	}
}

// Enter must still run the typed command. A picker with no rows must not own
// Enter, or "!cmd⏎" would need two presses.
func TestEnterRunsTheCommandBeforeAnyRankingArrives(t *testing.T) {
	c, bus := wiredComposer(t)
	c.SetBashPredictor(&fakeBashPredictor{waitForCancel: true})
	pressText(t, c, "!git status")
	require.False(t, c.bash.picker.Open, "nothing to show, nothing on screen")
	require.Empty(t, c.bash.picker.Items)

	pressKey(t, c, xui.KeyEvent{Code: xui.KeyEnter, Press: true})

	assert.Equal(t, "!git status", submittedText(t, bus), "Enter runs what the user typed")
}

// When the highlighted row is what the user already typed, accepting it would
// rewrite the composer with itself; Enter keeps its usual meaning.
func TestEnterRunsTheCommandWhenTheRowIsAlreadyTyped(t *testing.T) {
	c, bus := wiredComposer(t)
	c.SetBashPredictor(&fakeBashPredictor{})
	c.Chat.Value, c.Chat.Cursor = "!git status", len("!git status")
	c.bash.onChange(true, "git status")
	c.bash.picker.SetResults([]mention.Item{{Path: "git status"}, {Path: "git stash"}}, "")
	c.bash.show()

	pressKey(t, c, xui.KeyEvent{Code: xui.KeyEnter, Press: true})

	assert.Equal(t, "!git status", submittedText(t, bus))
	assert.Equal(t, "!git status", c.Chat.Value, "the composer is not rewritten with itself")
}

func TestEnterAcceptsTrailingSpaceCompletion(t *testing.T) {
	c, bus := wiredComposer(t)
	c.SetBashPredictor(&fakeBashPredictor{})
	c.Chat.Value, c.Chat.Cursor = "!git", len("!git")
	c.bash.picker.SetResults([]mention.Item{{Path: "git "}}, "")
	c.bash.show()

	pressKey(t, c, xui.KeyEvent{Code: xui.KeyEnter, Press: true})

	assert.Equal(t, "!git ", c.Chat.Value)
	assert.Empty(t, submittedText(t, bus), "the space starts an argument, rather than executing git")
}

func TestEnterAcceptsARowThatDiffersFromWhatWasTyped(t *testing.T) {
	c, bus := wiredComposer(t)
	c.SetBashPredictor(&fakeBashPredictor{})
	c.Chat.Value, c.Chat.Cursor = "!git s", len("!git s")
	c.bash.onChange(true, "git s")
	c.bash.picker.SetResults([]mention.Item{{Path: "git stash"}}, "")
	c.bash.show()

	pressKey(t, c, xui.KeyEvent{Code: xui.KeyEnter, Press: true})

	assert.Equal(t, "!git stash", c.Chat.Value, "Enter fills the highlighted command")
	assert.Empty(t, submittedText(t, bus), "filling is not running")
}

func TestNavigationKeysStayWithTheComposerUntilThereAreRows(t *testing.T) {
	c, _ := wiredComposer(t)
	c.SetBashPredictor(&fakeBashPredictor{waitForCancel: true})
	// Editing a multi-line command needs the cursor keys, so a picker with no
	// rows must not hold them.
	pressText(t, c, "!for f in *; do\necho done")
	c.Chat.Cursor = len(c.Chat.Value)
	require.False(t, c.bash.picker.Open, "nothing to show means nothing on screen")

	pressKey(t, c, xui.KeyEvent{Code: xui.KeyUp, Press: true})

	assert.Less(t, c.Chat.Cursor, len(c.Chat.Value), "Up moved the cursor inside the command")
}

// Typing on drops the rows of the previous query while the new ranking is in
// flight, and the picker with them: with nothing to navigate it is not on screen
// to swallow Enter either, so the keystroke after every ranking still needs one
// press to run the command.
func TestEnterRunsTheCommandWhileARerankIsInFlight(t *testing.T) {
	c, bus := wiredComposer(t)
	c.SetBashPredictor(&fakeBashPredictor{items: []controller.BashSuggestion{{Command: "git stash", Score: 0.9}}})
	c.Chat.Value, c.Chat.Cursor = "!git s", len("!git s")
	c.bash.onChange(true, "git s")
	c.ApplyBashSuggestions(drainBash(t, bus))
	require.True(t, c.Chat.BashOpen)

	c.SetBashPredictor(&fakeBashPredictor{waitForCancel: true})
	pressKey(t, c, xui.KeyEvent{Code: xui.KeyRune, Rune: 't', Press: true})
	require.False(t, c.bash.picker.Open, "the dropped rows take the picker with them")
	require.Empty(t, c.bash.picker.Items)
	require.False(t, c.Chat.BashOpen, "no rows means no key claim")

	pressKey(t, c, xui.KeyEvent{Code: xui.KeyEnter, Press: true})

	assert.Equal(t, "!git st", submittedText(t, bus), "Enter runs what the user typed")
	c.bash.hide()
}

// A failed judgement leaves a message where the rows were. The message is not
// something to navigate, so Enter still has to run the typed command.
func TestEnterRunsTheCommandAfterAFailedRanking(t *testing.T) {
	c, bus := wiredComposer(t)
	c.SetBashPredictor(&fakeBashPredictor{items: []controller.BashSuggestion{{Command: "git stash", Score: 0.9}}})
	c.Chat.Value, c.Chat.Cursor = "!git s", len("!git s")
	c.bash.onChange(true, "git s")
	c.ApplyBashSuggestions(drainBash(t, bus))
	require.True(t, c.Chat.BashOpen)

	c.ApplyBashSuggestions(controller.BashSuggestionsMsg{
		Gen:     c.bash.gen,
		Query:   "git s",
		ErrText: "Completions unavailable",
	})

	assert.Equal(t, "Completions unavailable", c.bash.picker.Status)
	assert.False(t, c.Chat.BashOpen, "a status row is not a row to navigate")

	pressKey(t, c, xui.KeyEvent{Code: xui.KeyEnter, Press: true})

	assert.Equal(t, "!git s", submittedText(t, bus))
}

// The judge ranks a list, so Down walks it and Tab completes the row it landed
// on — never runs it.
func TestBashPickerNavigatesTheRankedRows(t *testing.T) {
	c, bus := wiredComposer(t)
	c.SetBashPredictor(&fakeBashPredictor{items: []controller.BashSuggestion{
		{Command: "git stash", Score: 0.8, IsPrefix: true},
		{Command: "git status", Score: 0.5, IsPrefix: true},
	}})
	c.Chat.Value, c.Chat.Cursor = "!git st", len("!git st")
	c.bash.onChange(true, "git st")
	c.ApplyBashSuggestions(drainBash(t, bus))
	require.Len(t, c.bash.picker.Items, 2)

	pressKey(t, c, xui.KeyEvent{Code: xui.KeyDown, Press: true})
	require.Equal(t, 1, c.bash.picker.Selected, "Down walks the ranking")

	pressKey(t, c, xui.KeyEvent{Code: xui.KeyTab, Press: true})

	assert.Equal(t, "!git status", c.Chat.Value, "Tab completes the highlighted row")
	assert.Empty(t, submittedText(t, bus), "Tab never runs a command")
}

func TestSetThemeRestylesTheBashPicker(t *testing.T) {
	c := NewComposerPane(components.DefaultTheme(), "m", t.TempDir())
	th := components.DefaultTheme()
	th.Border = xui.Style{Reverse: true}

	c.SetTheme(th)

	assert.Equal(t, th, c.bash.picker.Theme)
}

func TestRepeatedQueryIsNotAskedTwice(t *testing.T) {
	// A cursor move reports the same query again: answering it again would burn
	// a round trip and flicker the rows away.
	predictor := &fakeBashPredictor{items: []controller.BashSuggestion{{Command: "git stash", Score: 0.9}}}
	c, bus := newBashPane(t, predictor)

	c.Chat.Value, c.Chat.Cursor = "!git s", len("!git s")
	c.bash.onChange(true, "git s")
	c.ApplyBashSuggestions(drainBash(t, bus))
	require.Len(t, c.bash.picker.Items, 1)

	c.bash.onChange(true, "git s")

	assert.Len(t, c.bash.picker.Items, 1, "the ranking already in hand is kept")
	assert.Equal(t, []string{"git s"}, predictor.asked(), "the judge is asked once per distinct query")
	assert.True(t, c.Chat.BashOpen)
}

// Esc aimed at a "!" command drops the judgement behind it instead of falling
// through to the branch that cancels the running turn: with nothing drawn yet,
// the picker's own state cannot tell the two apart.
func TestEscapeDropsAJudgementThatHasNotDrawnAnything(t *testing.T) {
	c, bus := wiredComposer(t)
	predictor := &fakeBashPredictor{waitForCancel: true}
	c.SetBashPredictor(predictor)

	pressText(t, c, "!git status")
	waitFor(t, func() bool { return len(predictor.asked()) == 1 })
	require.False(t, c.bash.picker.Open, "nothing is on screen to dismiss yet")

	pressKey(t, c, xui.KeyEvent{Code: xui.KeyEscape, Press: true})

	// A dropped request stays quiet: no ranking, no picker.
	time.Sleep(2 * bashSuggestDebounce)

	assert.Empty(t, bus.Drain(), "a dismissed judgement must not come back")
	assert.False(t, c.bash.picker.Open)
	assert.False(t, c.Chat.BashOpen)
	assert.Empty(t, c.bash.picker.Status)
}

// pressText types value through the composer's real key path, so the completer
// notifications fire the way a keystroke fires them.
func pressText(t *testing.T, c *ComposerPane, value string) {
	t.Helper()
	for _, r := range value {
		if r == '\n' {
			// A newline needs Shift+Enter; a bare Enter submits.
			pressKey(t, c, xui.KeyEvent{Code: xui.KeyEnter, Mods: xui.ModShift, Press: true})
			continue
		}
		pressKey(t, c, xui.KeyEvent{Code: xui.KeyRune, Rune: r, Press: true})
	}
}

// pressKey routes one key through the pane, as the app does.
func pressKey(t *testing.T, c *ComposerPane, ev xui.KeyEvent) {
	t.Helper()
	c.Handle(&components.EventContext{}, ev)
}

// waitFor polls until cond holds, so a debounced goroutine can be observed
// without a fixed sleep.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.Fail(t, "condition never held")
}
