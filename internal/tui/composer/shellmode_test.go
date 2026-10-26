package composer

import (
	"testing"
	"time"

	"github.com/pulseaiclub/xui"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// shellComposer builds a wired composer with a predictor that answers every
// query with one history row, the way most shell sessions look.
func shellComposer(t *testing.T) (*ComposerPane, *controller.Bus, *fakeBashPredictor) {
	t.Helper()
	predictor := &fakeBashPredictor{items: []controller.BashSuggestion{{Command: "git stash", Score: 0.9}}}
	c, bus := wiredComposer(t)
	c.SetBashPredictor(predictor)
	return c, bus, predictor
}

// A bare "!" on an empty composer enters shell mode: the state flips, the
// border takes the bash color, and the header names the mode so the empty
// composer is never mistaken for a prompt headed to the agent.
func TestBangOnEmptyEntersShellMode(t *testing.T) {
	c, _, _ := shellComposer(t)
	th := components.DefaultTheme()

	pressKey(t, c, xui.KeyEvent{Code: xui.KeyRune, Rune: '!', Press: true})

	assert.True(t, c.Chat.ShellMode)
	assert.True(t, c.ShellMode())
	assert.Equal(t, th.ToolName, c.Chat.BorderStyle, "the border names the mode")
	assert.Equal(t, "bash · ! exits", c.Chat.TopLeftLabel.Text)
	assert.Empty(t, c.Chat.Value, "the bang is consumed, not typed")
}

// The same "!" leaves the mode again: a terminal toggle, not a sticky trap.
func TestBangOnEmptyTogglesShellModeOff(t *testing.T) {
	c, _, _ := shellComposer(t)
	require.False(t, c.Chat.ShellMode)

	pressKey(t, c, xui.KeyEvent{Code: xui.KeyRune, Rune: '!', Press: true})
	require.True(t, c.Chat.ShellMode)
	pressKey(t, c, xui.KeyEvent{Code: xui.KeyRune, Rune: '!', Press: true})

	assert.False(t, c.Chat.ShellMode)
	assert.Empty(t, c.Chat.TopLeftLabel.Text)
	assert.Equal(t, components.DefaultTheme().Border, c.Chat.BorderStyle, "the border returns to chrome")
}

// With text on the buffer "!" keeps its old meaning: a literal character, and
// the start of a one-shot "!cmd" once it lands after other text.
func TestBangWithTextStaysLiteral(t *testing.T) {
	c, _, _ := shellComposer(t)

	pressText(t, c, "say hi")
	pressKey(t, c, xui.KeyEvent{Code: xui.KeyRune, Rune: '!', Press: true})

	assert.False(t, c.Chat.ShellMode)
	assert.Equal(t, "say hi!", c.Chat.Value, "the bang landed in the buffer")
}

// Modifiered bangs are shortcuts elsewhere: they never toggle the mode.
func TestModifiedBangDoesNotToggleShellMode(t *testing.T) {
	for _, mods := range []xui.Modifiers{xui.ModCtrl, xui.ModAlt, xui.ModShift} {
		c, _, _ := shellComposer(t)

		pressKey(t, c, xui.KeyEvent{Code: xui.KeyRune, Rune: '!', Mods: mods, Press: true})

		assert.False(t, c.Chat.ShellMode, "mods %v must not enter shell mode", mods)
	}
}

// Esc on an empty shell-mode composer leaves the mode; with text on the
// buffer Esc still belongs to the text, not to the mode.
func TestEscapeLeavesShellModeOnlyOnAnEmptyBuffer(t *testing.T) {
	c, _, _ := shellComposer(t)
	pressKey(t, c, xui.KeyEvent{Code: xui.KeyRune, Rune: '!', Press: true})
	require.True(t, c.Chat.ShellMode)

	pressText(t, c, "ls")
	pressKey(t, c, xui.KeyEvent{Code: xui.KeyEscape, Press: true})
	require.True(t, c.Chat.ShellMode, "Esc with text keeps the mode")
	require.Equal(t, "ls", c.Chat.Value, "Esc left the text alone")

	c.ClearInput()
	pressKey(t, c, xui.KeyEvent{Code: xui.KeyEscape, Press: true})

	assert.False(t, c.Chat.ShellMode, "Esc on the now-empty composer leaves the mode")
}

// Shell mode routes completion through the same predictor, but for the bare
// text: no bang to strip, so what the user typed is what gets ranked.
func TestShellModeCompletesBareText(t *testing.T) {
	c, bus, predictor := shellComposer(t)
	pressKey(t, c, xui.KeyEvent{Code: xui.KeyRune, Rune: '!', Press: true})
	require.True(t, c.Chat.ShellMode)

	pressText(t, c, "git s")
	waitFor(t, func() bool { return len(predictor.asked()) > 0 })
	c.ApplyBashSuggestions(drainBash(t, bus))

	assert.Equal(t, []string{"git s"}, predictor.asked(), "the bare text is the query")
	require.Len(t, c.bash.picker.Items, 1)
	assert.Equal(t, "git stash", c.bash.picker.Items[0].Path)

	// Tab accepts the row into the buffer without a bang appearing.
	pressKey(t, c, xui.KeyEvent{Code: xui.KeyTab, Press: true})
	time.Sleep(2 * bashSuggestDebounce)
	assert.Equal(t, "git stash", c.Chat.Value, "the accepted row carries no bang")
}

// Shell-mode submits are not prompt-recall material: Up after a run of
// commands must offer the last agent prompt, not replay a command on Enter.
func TestShellModeSubmitsSkipPromptHistory(t *testing.T) {
	c, _, _ := shellComposer(t)
	hist := &fakePromptHistory{}
	c.SetPromptHistory(hist)

	pressKey(t, c, xui.KeyEvent{Code: xui.KeyRune, Rune: '!', Press: true})
	pressText(t, c, "git status")
	c.rememberPrompt(c.Chat.Value)

	assert.Empty(t, hist.Entries(), "a shell-mode submit is a command, not a prompt")
}
