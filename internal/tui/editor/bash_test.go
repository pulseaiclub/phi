package editor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/tui/controller"
)

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
