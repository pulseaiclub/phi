package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/app"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/tui/commands"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/util/filesearch"
)

func newTestEditor(t *testing.T) *Editor {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\n\nfunc main() {}\n"), 0o600))
	return newShellTestEditor(t, dir)
}

// No vx: only fine because these paths never refresh the terminal.
func newShellTestEditor(t *testing.T, dir string) *Editor {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PHI_MODEL", "test-model")
	t.Setenv("PHI_API_KEY", "test-key")
	t.Setenv("PHI_BASE_URL", "http://127.0.0.1:9")
	t.Setenv("PHI_THINK_LEVEL", "")
	proj, err := project.Discover(dir)
	require.NoError(t, err)
	bus := controller.NewBus(nil)
	ctrl := newInjectedTestController(t, bus, proj)
	e := NewEditor(
		app.NewApp(nil),
		bus,
		ctrl,
		nil,
		components.DefaultTheme(),
		dir,
		"",
		nil,
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
	require.NotNil(t, e.codePlugin, "NewEditor must construct the code pane")

	require.True(t, e.commands.DispatchSlash("/code a.go", commands.NewContext(e.bus, nil)))

	require.True(t, e.codePlugin.Active())
	assert.True(t, e.blocksComposer(), "the overlay owns the keyboard")

	surf := frame(t, e)
	require.NotNil(t, surf.Widget, "an overlay surface must point back at the Editor")
	assert.Contains(t, components.SurfaceText(surf), "a.go")
	assert.Contains(t, components.SurfaceText(surf), "func main() {}")
}

func TestCodeCommandAcceptsLineSuffix(t *testing.T) {
	e := newTestEditor(t)
	require.True(t, e.commands.DispatchSlash("/code a.go:3", commands.NewContext(e.bus, nil)))
	require.True(t, e.codePlugin.Active())

	// Line 3 is `func main() {}`; the caret sits on the code, not in column 0.
	assert.Contains(t, components.SurfaceText(frame(t, e)), "a.go:3:1")
}

// An @ prefix is expanded to ./ so the shell can complete the path.
func TestCodeCommandExpandsAtPrefix(t *testing.T) {
	e := newTestEditor(t)
	require.True(t, e.commands.DispatchSlash("/code @a.go", commands.NewContext(e.bus, nil)))
	require.True(t, e.codePlugin.Active())
	assert.Contains(t, components.SurfaceText(frame(t, e)), "a.go")
}

func TestCodeCommandWithoutPathOpensPicker(t *testing.T) {
	if _, err := filesearch.ResolveFD(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "internal"), 0o755))
	writeDiffFile(t, dir, "a.go", "root file\n")
	writeDiffFile(t, dir, "internal/a.go", "nested file\n")
	e := diffEditor(t, dir)
	require.True(t, e.commands.DispatchSlash("/code", commands.NewContext(e.bus, nil)))
	assert.False(t, e.codePlugin.Active())
	text := strings.Join(strings.Fields(branchOverlay(t, e)), " ")
	assert.Contains(t, text, "Files")
	assert.Contains(t, text, "a.go .")
	assert.Contains(t, text, "a.go internal")
	for _, r := range "internal/a.go" {
		diffKey(e, xui.KeyRune, r)
	}
	ctx := diffKey(e, xui.KeyEnter, 0)
	assert.Same(t, e, ctx.Focus)
	require.True(t, e.codePlugin.Active())
	assert.Contains(t, components.SurfaceText(frame(t, e)), "nested file")
	diffKey(e, xui.KeyEscape, 0)
	assert.False(t, e.codePlugin.Active())
	assert.False(t, e.blocksComposer())
}

func TestCodePickerEmptyAndCancel(t *testing.T) {
	if _, err := filesearch.ResolveFD(); err != nil {
		t.Skip(err)
	}
	e := diffEditor(t, t.TempDir())
	e.diffPlugin.OpenText("", nil)
	e.openCode(nil)
	assert.False(t, e.diffPlugin.Active())
	assert.Contains(t, branchOverlay(t, e), "No files found")
	diffKey(e, xui.KeyEscape, 0)
	_, open := e.composer.ListOverlay(components.DrawContext{})
	assert.False(t, open)
	assert.False(t, e.codePlugin.Active())
}

func TestCodePickerOpensLiteralPath(t *testing.T) {
	if _, err := filesearch.ResolveFD(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	writeDiffFile(t, dir, "@file with spaces.go", "literal path\n")
	e := diffEditor(t, dir)
	e.openCode(nil)
	diffKey(e, xui.KeyEnter, 0)
	require.True(t, e.codePlugin.Active())
	assert.Contains(t, components.SurfaceText(frame(t, e)), "literal path")
}

func TestCodePickerSearchError(t *testing.T) {
	if _, err := filesearch.ResolveFD(); err != nil {
		t.Skip(err)
	}
	e := diffEditor(t, filepath.Join(t.TempDir(), "missing"))
	e.openCode(nil)
	assert.Contains(t, e.toastPlugin.Current().Message, "List files:")
	_, open := e.composer.ListOverlay(components.DrawContext{})
	assert.False(t, open)
	assert.False(t, e.codePlugin.Active())
}

func TestCodePaneTakesKeysAndReturnsFocus(t *testing.T) {
	e := newTestEditor(t)
	e.openCode([]string{"a.go"})
	require.True(t, e.codePlugin.Active())

	ctx := &components.EventContext{}
	e.Handle(ctx, xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: 'j'})
	assert.True(t, ctx.Consume, "the pane swallows the key")

	e.Handle(&components.EventContext{}, xui.KeyEvent{Press: true, Code: xui.KeyEscape})
	assert.False(t, e.codePlugin.Active())
	assert.False(t, e.blocksComposer(), "closing the pane hands the keyboard back")
}

func TestClosingCodePaneReleasesFocus(t *testing.T) {
	e := newTestEditor(t)
	e.openCode([]string{"a.go"})

	ctx := &components.EventContext{}
	e.Handle(ctx, xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: 'q'})
	assert.False(t, e.codePlugin.Active())
}

// TestThemeChangeReachesCodePane was dropped with the nil-collaborator guards:
// applyTheme ends in vx.QueueRefresh, which needs a real terminal.

// Openers must not leave the other overlay live underneath.
func TestOpeningCodeClosesDiff(t *testing.T) {
	e := newTestEditor(t)
	e.diffPlugin.OpenText("", nil)
	require.True(t, e.diffPlugin.Active())

	e.openCode([]string{"a.go"})
	assert.False(t, e.diffPlugin.Active())
	assert.True(t, e.codePlugin.Active())
}
