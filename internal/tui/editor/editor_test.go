package editor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// recordingComposer captures palette rebuilds so a test can see that the slash
// surface was re-derived.
type recordingComposer struct {
	updates int
	last    []palette.PaletteCommand
}

func (r *recordingComposer) SetPaletteCommands(cmds []palette.PaletteCommand) {
	r.updates++
	r.last = cmds
}

// Extensions can change underneath the palette (a reload, an extension unloading
// itself). The palette has to be re-derived, or it keeps advertising commands
// whose process is already gone.
func TestExtTopologyChangedResyncsSlashSurface(t *testing.T) {
	e := newTestEditor(t)
	require.NotNil(t, e.extCmds)

	rec := &recordingComposer{}
	e.extCmds.Composer = rec

	e.bus.Publish(controller.ExtTopologyChangedMsg{})
	e.drainBus()

	require.Equal(t, 1, rec.updates, "the palette must be rebuilt from the loaded extensions")
	assert.NotEmpty(t, rec.last, "the builtin surface survives the resync")
}

func TestShellCompletionWithoutHistory(t *testing.T) {
	e := newTestEditor(t)
	require.Empty(t, e.ctrl.SessionDir())
	e.composer.Chat.Value = "!git ch"
	e.composer.Chat.Cursor = len(e.composer.Chat.Value)
	e.composer.Chat.OnBashChange(true, "git ch")
	t.Cleanup(e.composer.HideCompleters)

	var result controller.BashSuggestionsMsg
	require.Eventually(t, func() bool {
		for _, msg := range e.bus.Drain() {
			if suggestions, ok := msg.(controller.BashSuggestionsMsg); ok {
				result = suggestions
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond)
	require.Empty(t, result.ErrText)
	require.NotEmpty(t, result.Items)
	assert.Contains(t, result.Items[0].Command, "git ch")
	e.Update(result)
	assert.True(t, e.composer.Chat.BashOpen)
}
