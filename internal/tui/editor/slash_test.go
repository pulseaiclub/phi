package editor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
)

// frame is defined in code_test.go.

// TestComposerSlashPickerSharesEditorRegistry walks the real key path: typing
// "/" in the composer must consult the editor's shared command registry. It
// pins the constructor wiring — a composer built without WithCommands
// nil-panics on the first keystroke, which no registry-level test catches.
func TestComposerSlashPickerSharesEditorRegistry(t *testing.T) {
	e := newTestEditor(t)

	typeRune := func(r rune) {
		t.Helper()
		ctx := &components.EventContext{}
		e.Handle(ctx, xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: r})
	}

	typeRune('/')
	require.True(t, e.composer.Chat.SlashOpen, "typing / must open the slash picker")

	typeRune('c')
	typeRune('o')

	// The slash picker renders inside the composer, not as a list overlay.
	surf := frame(t, e)
	assert.Contains(t, components.SurfaceText(surf), "/code")
}
