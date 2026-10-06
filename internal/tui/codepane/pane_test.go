package codepane

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chat"
)

// harness collects the pane's side effects so tests can assert on them.
type harness struct {
	pane   *Pane
	toasts []string
	refs   []chat.Ref
}

func newHarness(t *testing.T, files map[string]string) *harness {
	t.Helper()
	cwd := t.TempDir()
	for name, body := range files {
		path := filepath.Join(cwd, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	}
	h := &harness{}
	h.pane = New(
		components.DefaultTheme(),
		cwd,
		func(ref chat.Ref) { h.refs = append(h.refs, ref) },
		func(msg string) { h.toasts = append(h.toasts, msg) },
	)
	return h
}

func (h *harness) key(t *testing.T, r rune) {
	t.Helper()
	ctx := &components.EventContext{}
	h.pane.Handle(ctx, xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: r})
}

func (h *harness) code(t *testing.T, code xui.KeyCode) {
	t.Helper()
	h.pane.Handle(&components.EventContext{}, xui.KeyEvent{Press: true, Code: code})
}

func (h *harness) draw() components.Surface {
	return h.pane.Draw(components.DrawContext{
		Max:    components.Size{Width: 60, Height: 12},
		Method: xui.WidthUnicode,
	})
}

func TestOpenLoadsFileAndActivates(t *testing.T) {
	h := newHarness(t, map[string]string{"src/a.go": "package main\n\nfunc main() {}\n"})

	assert.False(t, h.pane.Active())
	h.pane.Open("src/a.go")
	require.True(t, h.pane.Active())
	assert.Equal(t, "src/a.go", h.pane.rel)
	assert.Equal(t, []string{"package main", "", "func main() {}"}, h.pane.lines)
	assert.NotEmpty(t, h.pane.hl, "a known language is syntax highlighted")
	assert.Contains(t, components.SurfaceText(h.draw()), "src/a.go")
}

func TestOpenAtPutsCursorOnLine(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\ntwo\nthree\n"})
	h.pane.OpenAt("a.go", 3)
	assert.Equal(t, 2, h.pane.line)
}

func TestOpenAtClampsPastEnd(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\ntwo\n"})
	h.pane.OpenAt("a.go", 99)
	assert.Equal(t, 1, h.pane.line)
}

func TestOpenMissingFileShowsError(t *testing.T) {
	h := newHarness(t, nil)
	h.pane.Open("nope.go")
	require.True(t, h.pane.Active())
	assert.Contains(t, h.pane.loadErr, "no such file")
	assert.Contains(t, components.SurfaceText(h.draw()), "no such file")
}

func TestOpenRefusesBinaryAndDirectories(t *testing.T) {
	h := newHarness(t, nil)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "blob.bin"), []byte{'a', 0, 'b'}, 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o750))

	h.pane.Open(filepath.Join(dir, "blob.bin"))
	assert.Contains(t, h.pane.loadErr, "binary")

	h.pane.Open(filepath.Join(dir, "sub"))
	assert.Contains(t, h.pane.loadErr, "is a directory")
}

func TestEscapeClosesPane(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\n"})
	h.pane.Open("a.go")
	h.code(t, xui.KeyEscape)
	assert.False(t, h.pane.Active())
}

func TestMoveAndJumpKeys(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\ntwo\nthree\n"})
	h.pane.Open("a.go")

	h.key(t, 'j')
	assert.Equal(t, 1, h.pane.line)
	h.key(t, 'k')
	assert.Equal(t, 0, h.pane.line)
	h.key(t, 'G')
	assert.Equal(t, 2, h.pane.line)
	h.key(t, 'g')
	h.key(t, 'g')
	assert.Equal(t, 0, h.pane.line)
}

func TestHorizontalKeysMoveTheCaret(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "0123456789\n"})
	h.pane.Open("a.go")

	h.key(t, 'l')
	assert.Equal(t, 1, h.pane.col, "l steps right")
	h.key(t, 'h')
	assert.Equal(t, 0, h.pane.col)
	h.key(t, 'h')
	assert.Equal(t, 0, h.pane.col, "h stops at the start of the line")

	h.key(t, '$')
	assert.Equal(t, 10, h.pane.col)
	h.key(t, 'l')
	assert.Equal(t, 10, h.pane.col, "l stops at the end of the line")
	h.key(t, '0')
	assert.Equal(t, 0, h.pane.col)
}

func TestArrowKeysMoveTheCaret(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "0123456789\n"})
	h.pane.Open("a.go")

	h.code(t, xui.KeyRight)
	assert.Equal(t, 1, h.pane.col)
	h.code(t, xui.KeyLeft)
	assert.Equal(t, 0, h.pane.col)
	h.code(t, xui.KeyEnd)
	assert.Equal(t, 10, h.pane.col)
	h.code(t, xui.KeyHome)
	assert.Equal(t, 0, h.pane.col)
}

func TestColumnCursorStopsAtRuneBoundaries(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "日本語\n"})
	h.pane.Open("a.go")

	h.code(t, xui.KeyRight)
	assert.Equal(t, 3, h.pane.col, "one arrow crosses one rune, not one byte")
	h.code(t, xui.KeyRight)
	assert.Equal(t, 6, h.pane.col)
	h.key(t, '$')
	assert.Equal(t, 9, h.pane.col)
	h.code(t, xui.KeyRight)
	assert.Equal(t, 9, h.pane.col, "the caret stops at end of line")
	h.key(t, 'h')
	assert.Equal(t, 6, h.pane.col, "h crosses one rune backwards")
}

// A long line must not push the caret off the right edge: the view follows it.
func TestCaretScrollsIntoView(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "x" + strings.Repeat("y", 120) + "\n"})
	h.pane.Open("a.go")
	h.draw() // sets the viewport width

	h.key(t, '$')
	codeW := h.pane.viewW - gutterWidth(len(h.pane.lines))
	require.Positive(t, h.pane.xScroll, "the caret at end of line scrolls the view")
	assert.Less(t, displayCol(h.pane.lineText(), h.pane.col, xui.WidthUnicode)-h.pane.xScroll, codeW)

	h.key(t, 'g')
	h.key(t, 'g')
	assert.Equal(t, 0, h.pane.xScroll, "gg returns to the origin on both axes")
}

func TestScrollFollowsCaretDown(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\ntwo\nthree\nfour\nfive\nsix\n"})
	h.pane.Open("a.go")
	h.pane.viewH = 3

	for range 5 {
		h.key(t, 'j')
	}
	assert.Equal(t, 5, h.pane.line)
	assert.Equal(t, 3, h.pane.scroll, "the caret line stays inside the body")
}

func TestInactivePaneIgnoresInput(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\ntwo\n"})
	ctx := &components.EventContext{}
	h.pane.Handle(ctx, xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: 'j'})
	assert.False(t, ctx.Consume)
	assert.False(t, h.pane.Active())
}

func TestDrawSetsCursorOnCaretCell(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\ntwo\n"})
	h.pane.OpenAt("a.go", 2)
	h.code(t, xui.KeyRight)

	surf := h.draw()
	require.NotNil(t, surf.Cursor)
	assert.Equal(t, 2, surf.Cursor.Y, "line index 1 is the second body row")
	assert.Equal(t, gutterWidth(2)+1, surf.Cursor.X)
}

func TestDrawReportsCaretInStatus(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\ntwo\n"})
	h.pane.OpenAt("a.go", 2)
	assert.Contains(t, components.SurfaceText(h.draw()), "a.go:2:1")
}

// A jump must land on the code, not in the indentation.
func TestJumpLandsOnFirstNonBlank(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "package main\n\n\t\tfunc deep() {}\n"})
	h.pane.OpenAt("a.go", 3)
	assert.Equal(t, 2, h.pane.col)
}

func TestSelectAndAddRef(t *testing.T) {
	h := newHarness(t, map[string]string{"src/a.go": "one\ntwo\nthree\n"})
	h.pane.OpenAt("src/a.go", 2)

	h.key(t, 'v')
	h.key(t, 'j')
	assert.True(t, h.pane.selecting)
	assert.Contains(t, components.SurfaceText(h.draw()), "2 lines selected")

	h.key(t, 'a')
	require.Len(t, h.refs, 1)
	ref := h.refs[0]
	assert.Equal(t, "src/a.go", ref.Path)
	assert.Equal(t, 2, ref.Start)
	assert.Equal(t, 3, ref.End)
	assert.Equal(t, "go", ref.Lang)
	assert.Equal(t, "two\nthree", ref.Text)
	assert.False(t, h.pane.selecting, "adding a ref drops the selection")
	assert.Contains(t, h.toasts, "added src/a.go:2-3 to chat")
}

// The status row counts what v picked, singular included.
func TestSelectionStatusCountsLines(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\ntwo\n"})
	h.pane.Open("a.go")

	h.key(t, 'v')
	assert.Contains(t, components.SurfaceText(h.draw()), "1 line selected")

	h.key(t, 'j')
	assert.Contains(t, components.SurfaceText(h.draw()), "2 lines selected")
}

// A load failure is what the user needs to see, selection or not.
func TestSelectionDoesNotHideLoadError(t *testing.T) {
	h := newHarness(t, nil)
	h.pane.Open("nope.go")

	h.key(t, 'v')
	text := components.SurfaceText(h.draw())
	assert.Contains(t, text, "no such file")
	assert.NotContains(t, text, "selected")
}

// Without v, a adds the caret line alone.
func TestAddRefWithoutSelectionUsesTheCaretLine(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\ntwo\n"})
	h.pane.OpenAt("a.go", 2)

	h.key(t, 'a')
	require.Len(t, h.refs, 1)
	assert.Equal(t, 2, h.refs[0].Start)
	assert.Equal(t, 2, h.refs[0].End)
	assert.Equal(t, "two", h.refs[0].Text)
}

func TestEscapeCancelsSelectionBeforeClosing(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\ntwo\n"})
	h.pane.Open("a.go")

	h.key(t, 'v')
	h.code(t, xui.KeyEscape)
	assert.False(t, h.pane.selecting)
	assert.True(t, h.pane.Active(), "escape drops the selection first, not the pane")

	h.code(t, xui.KeyEscape)
	assert.False(t, h.pane.Active())
}

// The viewer answers to the same deny list as the tool gate.
func TestRefusesSensitivePath(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	h := newHarness(t, nil)
	h.pane.Open(filepath.Join(home, ".ssh", "id_rsa"))
	assert.Contains(t, h.pane.loadErr, "sensitive")
}

func TestDisplayColCountsWideGlyphs(t *testing.T) {
	assert.Equal(t, 0, displayCol("日本", 0, xui.WidthUnicode))
	assert.Equal(t, 2, displayCol("日本", len("日"), xui.WidthUnicode))
	assert.Equal(t, 4, displayCol("日本", 99, xui.WidthUnicode))
}

func TestClampColSnapsToRuneBoundary(t *testing.T) {
	assert.Equal(t, 0, clampCol("日本", 1), "a byte inside a rune snaps back")
	assert.Equal(t, 3, clampCol("日本", 3))
	assert.Equal(t, 6, clampCol("日本", 99))
}

func TestRelPathFallsBackToAbsoluteOutsideCwd(t *testing.T) {
	cwd := t.TempDir()
	assert.Equal(t, "a/b.go", relPath(cwd, filepath.Join(cwd, "a", "b.go")))
	outside := filepath.Join(t.TempDir(), "x.go")
	// The fallback feeds titles and the status row, so it renders with forward
	// slashes too, not the os-native form.
	assert.Equal(t, filepath.ToSlash(outside), relPath(cwd, outside))
}

func TestHumanBytes(t *testing.T) {
	assert.Equal(t, "512 B", humanBytes(512))
	assert.Equal(t, "1.0 KB", humanBytes(1024))
	assert.Equal(t, "8.0 MB", humanBytes(maxFileBytes))
}

func TestFirstNonBlank(t *testing.T) {
	assert.Equal(t, 0, firstNonBlank(""))
	assert.Equal(t, 0, firstNonBlank("func f()"))
	assert.Equal(t, 3, firstNonBlank("   "), "an all-blank line parks at its end")
	assert.Equal(t, 2, firstNonBlank("  x"))
	assert.Equal(t, 3, firstNonBlank(" \t\tx"))
	assert.Equal(t, 2, firstNonBlank("  日"))
}

func TestSearchModeAndNextPrev(t *testing.T) {
	h := newHarness(t, map[string]string{
		"a.go": "alpha one\nbeta two\nalpha three\n",
	})
	h.pane.OpenAt("a.go", 2) // between the two matches

	h.key(t, '/')
	assert.True(t, h.pane.searchMode)

	for _, r := range "alpha" {
		h.key(t, r)
	}
	text := components.SurfaceText(h.draw())
	assert.Contains(t, text, "/alpha")
	assert.Contains(t, text, "2/2")
	assert.Equal(t, 2, h.pane.line, "the query lands on the first match below where / was pressed")

	h.code(t, xui.KeyEnter)
	assert.False(t, h.pane.searchMode)
	// Enter keeps the query for n/N, so the location has to stay next to it.
	text = components.SurfaceText(h.draw())
	assert.Contains(t, text, "a.go:3:1")
	assert.Contains(t, text, "2/2")

	h.key(t, 'n')
	assert.Equal(t, 0, h.pane.line, "n at the last match wraps to the first")
	assert.Contains(t, components.SurfaceText(h.draw()), "1/2")

	h.key(t, 'N')
	assert.Equal(t, 2, h.pane.line, "N at the first match wraps to the last")
	assert.Contains(t, components.SurfaceText(h.draw()), "2/2")

	// The counter follows the caret: off a match there is no position in the
	// match list to report, and saying nothing beats a stale "2/2".
	h.key(t, 'k')
	assert.Equal(t, 1, h.pane.line)
	text = components.SurfaceText(h.draw())
	assert.Contains(t, text, "a.go:2:1")
	assert.NotContains(t, text, "1/2")
	assert.NotContains(t, text, "2/2")

	h.key(t, 'n')
	assert.Equal(t, 2, h.pane.line)

	// Selection extends with search match navigation
	h.key(t, 'v')
	h.key(t, 'N')
	assert.True(t, h.pane.selecting)
	assert.Equal(t, 0, h.pane.line)
	lo, hi := h.pane.selectionLines()
	assert.Equal(t, 0, lo)
	assert.Equal(t, 2, hi)
}

func TestSearchWrapsToTheTopFromBelow(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "alpha one\nbeta two\ngamma three\n"})
	h.pane.OpenAt("a.go", 3)

	h.key(t, '/')
	for _, r := range "alpha" {
		h.key(t, r)
	}
	assert.Equal(t, 0, h.pane.line, "nothing matches below: the top is the next match")
	assert.Contains(t, components.SurfaceText(h.draw()), "1/1")
}

func TestNextMatchStepsFromTheCaret(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "m0\nm1\nm2\nm3\nm4\n"})
	h.pane.Open("a.go")

	h.key(t, '/')
	h.key(t, 'm')
	h.code(t, xui.KeyEnter)
	require.Equal(t, 0, h.pane.line)

	for range 3 {
		h.key(t, 'j')
	}
	require.Equal(t, 3, h.pane.line)

	h.key(t, 'n')
	assert.Equal(t, 4, h.pane.line, "n steps from the caret, not from the last match it visited")
	h.key(t, 'n')
	assert.Equal(t, 0, h.pane.line, "and wraps at the end of the file")
	h.key(t, 'N')
	assert.Equal(t, 4, h.pane.line)
}

func TestSearchEscapeRestoresTheCaret(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "alpha one\nbeta two\nalpha three\n"})
	h.pane.Open("a.go")
	h.key(t, 'j')
	h.key(t, 'l')
	before := h.pane.pos()

	h.key(t, '/')
	for _, r := range "alpha" {
		h.key(t, r)
	}
	require.Equal(t, 2, h.pane.line, "typing walks the caret to the match")

	h.code(t, xui.KeyEscape)
	assert.Equal(t, before, h.pane.pos())
	assert.Empty(t, h.pane.searchQuery)
	text := components.SurfaceText(h.draw())
	assert.Contains(t, text, "a.go:2:2")
	assert.NotContains(t, text, "1/2")
}

func TestSearchEscapeRestoresTheScroll(t *testing.T) {
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = "filler"
	}
	lines[25] = "needle"
	h := newHarness(t, map[string]string{"a.go": strings.Join(lines, "\n") + "\n"})
	h.pane.Open("a.go")
	h.draw() // the viewport is only known once a frame has been painted

	h.key(t, '/')
	for _, r := range "needle" {
		h.key(t, r)
	}
	require.Equal(t, 25, h.pane.line)
	require.Positive(t, h.pane.scroll, "the match is scrolled into view")

	h.code(t, xui.KeyEscape)
	assert.Equal(t, 0, h.pane.line)
	assert.Equal(t, 0, h.pane.scroll, "Esc gives the scroll back too")
}

func TestSearchWithNothingToRepeat(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "alpha\n"})
	h.pane.Open("a.go")

	h.key(t, 'n')
	assert.Empty(t, h.toasts, "n with no query to repeat does nothing")

	h.key(t, '/')
	for _, r := range "zzz" {
		h.key(t, r)
	}
	assert.Contains(t, components.SurfaceText(h.draw()), "no matches")

	h.code(t, xui.KeyEnter)
	h.key(t, 'n')
	assert.Equal(t, []string{"no matches"}, h.toasts)
	assert.Contains(t, components.SurfaceText(h.draw()), "a.go:1:1")
}

func TestSearchScansOneLowercaseCopy(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "Alpha One\nbeta two\n"})
	h.pane.Open("a.go")
	assert.Nil(t, h.pane.lowered, "no lowercase copy before the first search")

	h.key(t, '/')
	h.key(t, 'a')
	require.Len(t, h.pane.lowered, len(h.pane.lines))
	assert.Equal(t, "alpha one", h.pane.lowered[0])

	// Reopening drops the copy: it belongs to the lines it was built from.
	h.pane.Open("a.go")
	assert.Nil(t, h.pane.lowered)
}

func TestSelectParagraph(t *testing.T) {
	h := newHarness(t, map[string]string{
		"a.go": "line 1\nline 2\n\nline 3\nline 4\n",
	})
	h.pane.Open("a.go")

	h.key(t, 'j') // inside the first paragraph
	h.key(t, 'p')
	assert.True(t, h.pane.selecting)
	lo, hi := h.pane.selectionLines()
	assert.Equal(t, 0, lo)
	assert.Equal(t, 1, hi)

	h.key(t, 'p')
	assert.False(t, h.pane.selecting, "p again drops the paragraph")

	h.pane.line = 4 // the second paragraph
	h.key(t, 'p')
	lo, hi = h.pane.selectionLines()
	assert.Equal(t, 3, lo)
	assert.Equal(t, 4, hi)

	h.key(t, 'a')
	require.Len(t, h.refs, 1)
	assert.Equal(t, 4, h.refs[0].Start)
	assert.Equal(t, 5, h.refs[0].End)
}

func TestGotoLine(t *testing.T) {
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = "content"
	}
	h := newHarness(t, map[string]string{
		"a.go": strings.Join(lines, "\n") + "\n",
	})
	h.pane.Open("a.go")

	h.key(t, ':')
	assert.True(t, h.pane.gotoMode)

	h.key(t, '2')
	h.key(t, 'x') // only digits go into the buffer
	h.key(t, '5')
	text := components.SurfaceText(h.draw())
	assert.Contains(t, text, ":25")
	assert.Contains(t, text, "50 lines", "the prompt shows the range a number may land in")

	h.code(t, xui.KeyEnter)
	assert.False(t, h.pane.gotoMode)
	assert.Equal(t, 24, h.pane.line)

	// Selection extends with goto line
	h.key(t, 'v')
	h.key(t, ':')
	h.key(t, '3')
	h.key(t, '0')
	h.code(t, xui.KeyEnter)
	assert.True(t, h.pane.selecting)
	lo, hi := h.pane.selectionLines()
	assert.Equal(t, 24, lo)
	assert.Equal(t, 29, hi)

	h.code(t, xui.KeyEscape) // drop the selection, not the pane
	require.False(t, h.pane.selecting)

	// Past the end lands on the last line; a bare : goes nowhere.
	h.key(t, ':')
	for range 3 {
		h.key(t, '9')
	}
	h.code(t, xui.KeyEnter)
	assert.Equal(t, 49, h.pane.line)

	h.key(t, ':')
	h.code(t, xui.KeyEnter)
	assert.Equal(t, 49, h.pane.line)
}

func TestReopenDropsPromptState(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "alpha\nbeta\n"})
	h.pane.Open("a.go")

	h.key(t, '/')
	h.key(t, 'a')
	h.code(t, xui.KeyEnter)
	require.Equal(t, "a", h.pane.searchQuery)

	h.pane.Close()
	assert.False(t, h.pane.searchMode)
	assert.Empty(t, h.pane.searchQuery)
	assert.Nil(t, h.pane.matches)

	h.pane.Open("a.go")
	h.key(t, ':')
	h.key(t, '1')
	require.True(t, h.pane.gotoMode)

	h.pane.Open("a.go")
	assert.False(t, h.pane.gotoMode)
	assert.Empty(t, h.pane.gotoBuf)
}
