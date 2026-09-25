package editor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/tui/commands"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// newTestEditor builds the shell the way cmd does, with no engine behind it.
func newTestEditor(t *testing.T) *Editor {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\n\nfunc main() {}\n"), 0o600))

	e := NewEditor(
		nil,
		controller.NewBus(nil),
		nil,
		nil,
		components.DefaultTheme(),
		dir,
		"",
		"",
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

// An @ prefix is the picker's sigil, not part of the path: a relative
// remainder still resolves against cwd, so it opens the same file.
func TestCodeCommandExpandsAtPrefix(t *testing.T) {
	e := newTestEditor(t)
	require.True(t, e.commands.DispatchSlash("/code @a.go", commands.NewContext(e.bus, nil)))
	require.True(t, e.code.Active())
	assert.Contains(t, components.SurfaceText(frame(t, e)), "a.go:1:1")
}

// An absolute @ path must keep its root. Prefixing "./" — as the pane did for
// every @ argument — turns "C:\src\a.go" into a path under cwd, which is how
// the Windows picker's result came back as "no such file".
func TestCodeCommandKeepsAbsoluteAtPath(t *testing.T) {
	e := newTestEditor(t)
	abs := filepath.Join(e.cwd, "a.go")
	require.True(t, filepath.IsAbs(abs), "the fixture must exercise the absolute branch")

	e.openCode([]string{"@" + abs})

	require.True(t, e.code.Active())
	assert.Contains(t, components.SurfaceText(frame(t, e)), "a.go:1:1")
}

// A Windows drive letter carries a colon of its own, so the line suffix is the
// tail after the last one — not the fragment after "C".
func TestSplitLineSuffix(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		path string
		line int
	}{
		{name: "no suffix", arg: "a.go", path: "a.go"},
		{name: "line", arg: "a.go:3", path: "a.go", line: 3},
		{name: "nested path", arg: "src/a.go:42", path: "src/a.go", line: 42},
		{name: "drive path", arg: `C:\src\a.go`, path: `C:\src\a.go`},
		{name: "drive path with line", arg: `C:\src\a.go:12`, path: `C:\src\a.go`, line: 12},
		{name: "forward-slash drive path with line", arg: "C:/src/a.go:12", path: "C:/src/a.go", line: 12},
		{name: "non-numeric tail", arg: "a.go:x", path: "a.go:x"},
		{name: "zero line", arg: "a.go:0", path: "a.go:0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, line := splitLineSuffix(tt.arg)
			assert.Equal(t, tt.path, path)
			assert.Equal(t, tt.line, line)
		})
	}
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

func TestThemeChangeReachesCodePane(t *testing.T) {
	e := newTestEditor(t)
	e.openCode([]string{"a.go"})
	before := frame(t, e).Buffer

	e.applyTheme("pink")
	after := frame(t, e).Buffer
	assert.NotEqual(t, before, after, "the code pane must repaint after a theme change")
	assert.Equal(
		t,
		components.SurfaceText(components.Surface{Buffer: before}),
		components.SurfaceText(components.Surface{Buffer: after}),
		"a repaint must not change the text, only the colors",
	)
}

// Openers must not leave the other overlay live underneath.
func TestOpeningCodeClosesDiff(t *testing.T) {
	e := newTestEditor(t)
	e.diff.OpenText("", nil)
	require.True(t, e.diff.Active())

	e.openCode([]string{"a.go"})
	assert.False(t, e.diff.Active())
	assert.True(t, e.code.Active())
}
