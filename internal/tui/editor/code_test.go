package editor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/app"
	"github.com/pulseaiclub/phi/internal/tui/commands"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// newTestEditor builds the shell the way cmd does:
// headless App, live bus, zero-value controller, and an empty session directory.
// No vx: only fine because these paths never refresh the terminal.
func newTestEditor(t *testing.T) *Editor {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\n\nfunc main() {}\n"), 0o600))

	e := NewEditor(
		app.NewApp(nil),
		controller.NewBus(nil),
		&controller.EngineController{},
		nil,
		components.DefaultTheme(),
		dir,
		"",
		nil,
		0,
		nil,
	)
	require.NotNil(t, e)
	return e
}

func frame(t *testing.T, e *Editor) components.Surface {
	t.Helper()
	return e.Draw(components.DrawContext{
		Max:    components.Size{Width: 100, Height: 30},
		Method: xui.WidthUnicode,
	})
}

// The pane has to exist: a /code that silently does nothing is the failure this
// test exists to catch.
func TestCodeCommandOpensPane(t *testing.T) {
	e := newTestEditor(t)
	require.NotNil(t, e.code, "NewEditor must construct the code pane")

	require.True(t, e.commands.DispatchSlash("/code a.go", commands.NewContext(e.bus, nil)))

	require.True(t, e.code.Active())
	assert.True(t, e.blocksComposer(), "the overlay owns the keyboard")

	surf := frame(t, e)
	require.NotNil(t, surf.Widget, "an overlay surface must point back at the Editor")
	assert.Contains(t, components.SurfaceText(surf), "a.go")
	assert.Contains(t, components.SurfaceText(surf), "func main() {}")
}

func TestCodeCommandAcceptsLineSuffix(t *testing.T) {
	e := newTestEditor(t)
	require.True(t, e.commands.DispatchSlash("/code a.go:3", commands.NewContext(e.bus, nil)))
	require.True(t, e.code.Active())

	// Line 3 is `func main() {}`; the caret sits on the code, not in column 0.
	assert.Contains(t, components.SurfaceText(frame(t, e)), "a.go:3:1")
}

// An @ prefix is expanded to ./ so the shell can complete the path.
func TestCodeCommandExpandsAtPrefix(t *testing.T) {
	e := newTestEditor(t)
	require.True(t, e.commands.DispatchSlash("/code @a.go", commands.NewContext(e.bus, nil)))
	require.True(t, e.code.Active())
	assert.Contains(t, components.SurfaceText(frame(t, e)), "a.go")
}

// With no path the command explains itself instead of opening an empty pane.
func TestCodeCommandWithoutPathToasts(t *testing.T) {
	e := newTestEditor(t)
	require.True(t, e.commands.DispatchSlash("/code", commands.NewContext(e.bus, nil)))
	assert.False(t, e.code.Active())
}

func TestCodePaneTakesKeysAndReturnsFocus(t *testing.T) {
	e := newTestEditor(t)
	e.openCode([]string{"a.go"})
	require.True(t, e.code.Active())

	ctx := &components.EventContext{}
	e.Handle(ctx, xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: 'j'})
	assert.True(t, ctx.Consume, "the pane swallows the key")

	e.Handle(&components.EventContext{}, xui.KeyEvent{Press: true, Code: xui.KeyEscape})
	assert.False(t, e.code.Active())
	assert.False(t, e.blocksComposer(), "closing the pane hands the keyboard back")
}

func TestClosingCodePaneReleasesFocus(t *testing.T) {
	e := newTestEditor(t)
	e.openCode([]string{"a.go"})

	ctx := &components.EventContext{}
	e.Handle(ctx, xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: 'q'})
	assert.False(t, e.code.Active())
}

// TestThemeChangeReachesCodePane was dropped with the nil-collaborator guards:
// applyTheme ends in vx.QueueRefresh, which needs a real terminal.

// Openers must not leave the other overlay live underneath.
func TestOpeningCodeClosesDiff(t *testing.T) {
	e := newTestEditor(t)
	e.diff.OpenText("", nil)
	require.True(t, e.diff.Active())

	e.openCode([]string{"a.go"})
	assert.False(t, e.diff.Active())
	assert.True(t, e.code.Active())
}
