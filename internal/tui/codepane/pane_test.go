package codepane

import (
	"os"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/util/lsp"
)

// harness collects the pane's side effects so tests can assert on them.
type harness struct {
	pane   *Pane
	toasts []string
	copies []string
	copyOK bool
	wakes  int
}

func newHarness(t *testing.T, files map[string]string) *harness {
	t.Helper()
	cwd := t.TempDir()
	for name, body := range files {
		path := filepath.Join(cwd, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	}
	h := &harness{copyOK: true}
	h.pane = New(
		components.DefaultTheme(),
		cwd,
		nil, // no language server: the whole LSP surface must degrade politely
		nil, // onRef: tests that add refs will wire their own
		func(text string) bool {
			h.copies = append(h.copies, text)
			return h.copyOK
		},
		func(msg string) { h.toasts = append(h.toasts, msg) },
		func() { h.wakes++ },
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

func TestHorizontalScrollAndColumnCursor(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "0123456789\n"})
	h.pane.Open("a.go")
	h.pane.viewW = 40

	h.key(t, 'l')
	assert.Equal(t, scrollStep, h.pane.xScroll, "l scrolls the viewport")
	h.key(t, 'h')
	assert.Equal(t, 0, h.pane.xScroll)

	h.code(t, xui.KeyRight)
	assert.Equal(t, 1, h.pane.col, "arrow moves the caret")
	h.code(t, xui.KeyLeft)
	assert.Equal(t, 0, h.pane.col)
	h.key(t, '$')
	assert.Equal(t, 10, h.pane.col)
	h.key(t, '0')
	assert.Equal(t, 0, h.pane.col)
}

func TestColumnCursorStopsAtRuneBoundaries(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "日本語\n"})
	h.pane.Open("a.go")
	h.pane.viewW = 40

	h.code(t, xui.KeyRight)
	assert.Equal(t, 3, h.pane.col, "one arrow crosses one rune, not one byte")
	h.code(t, xui.KeyRight)
	assert.Equal(t, 6, h.pane.col)
	h.key(t, '$')
	assert.Equal(t, 9, h.pane.col)
	h.code(t, xui.KeyRight)
	assert.Equal(t, 9, h.pane.col, "the caret stops at end of line")
}

func TestSearchAndMatchNavigation(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "alpha\nbeta\nalpha two\n"})
	h.pane.Open("a.go")

	h.key(t, '/')
	for _, r := range "alpha" {
		ctx := &components.EventContext{}
		h.pane.Handle(ctx, xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: r})
	}
	require.Len(t, h.pane.matches, 2)
	assert.Equal(t, 0, h.pane.line)

	h.code(t, xui.KeyEnter)
	assert.False(t, h.pane.searchMode)
	h.key(t, 'n')
	assert.Equal(t, 2, h.pane.line)
	h.key(t, 'N')
	assert.Equal(t, 0, h.pane.line)
}

func TestCopyLineAndLocation(t *testing.T) {
	h := newHarness(t, map[string]string{"src/a.go": "one\ntwo\n"})
	h.pane.OpenAt("src/a.go", 2)

	h.key(t, 'y')
	assert.Equal(t, []string{"two"}, h.copies)

	h.key(t, 'Y')
	assert.Equal(t, []string{"two", "src/a.go:2"}, h.copies)
}

func TestCopyFailureIsReported(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\n"})
	h.copyOK = false
	h.pane.Open("a.go")
	h.key(t, 'y')
	assert.Contains(t, h.toasts, "copy failed")
}

// Without a language server every query must say so instead of hanging.
func TestLSPActionsWithoutManager(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "package main\n"})
	h.pane.Open("a.go")

	assert.Equal(t, "no language server support", h.pane.definition())
	assert.Equal(t, "no language server support", h.pane.hover())
	assert.Equal(t, "no language server support", h.pane.outlineSymbols())
	assert.Empty(t, h.pane.status, "a refused query must not leave a pending status")
}

func TestLSPActionsWithoutOpenFile(t *testing.T) {
	h := newHarness(t, nil)
	h.pane.mgr = lsp.New(t.TempDir(), false)
	h.pane.active = true
	assert.Equal(t, "no file open", h.pane.definition())
}

// land is the async completion path: one hit jumps, several queue the picker.
func TestLandSingleHitJumps(t *testing.T) {
	h := newHarness(t, map[string]string{})
	require.NoError(t, os.WriteFile(filepath.Join(h.pane.cwd, "target.go"), []byte("a\nb\nc\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(h.pane.cwd, "origin.go"), []byte("x\n"), 0o600))
	h.pane.Open("origin.go")

	h.pane.land(lspQuery{abs: h.pane.abs, rel: h.pane.rel, id: h.pane.req}, "definition",
		[]lsp.Hit{{Path: "target.go", Line: 3}}, nil)
	assert.Equal(t, "target.go", h.pane.rel)
	assert.Equal(t, 2, h.pane.line)
	assert.Equal(t, 1, h.wakes, "the async result asks for a repaint")
	assert.False(t, h.pane.pushPicker)
}

func TestLandMultipleHitsQueuePicker(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\ntwo\n"})
	h.pane.Open("a.go")

	h.pane.land(lspQuery{abs: h.pane.abs, rel: h.pane.rel, id: h.pane.req}, "references", []lsp.Hit{
		{Path: "a.go", Line: 1, Mid: "one"},
		{Path: "a.go", Line: 2, Mid: "two"},
	}, nil)
	assert.True(t, h.pane.pushPicker, "several hits wait for the UI goroutine to open the picker")

	surf := h.draw() // Draw opens the picker and paints it
	assert.False(t, h.pane.pushPicker)
	require.True(t, h.pane.picker.Open)
	require.Len(t, surf.Children, 1)
	assert.Contains(t, components.SurfaceText(surf.Children[0].Surface), "a.go:2")
}

func TestLandEmptyAndError(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\n"})
	h.pane.Open("a.go")

	h.pane.land(lspQuery{id: h.pane.req}, "definition", nil, nil)
	assert.Equal(t, "definition: no results", h.pane.status)
	assert.False(t, h.pane.pushPicker)

	h.pane.land(lspQuery{id: h.pane.req}, "definition", nil, assert.AnError)
	assert.Contains(t, h.pane.status, "definition:")
	assert.Equal(t, 2, h.wakes)
}

func TestAcceptPickerJumpsToHit(t *testing.T) {
	h := newHarness(t, map[string]string{"other.go": "a\nb\n", "a.go": "one\n"})
	h.pane.Open("a.go")
	h.pane.navHits = []lsp.Hit{{Path: "other.go", Line: 2}}

	h.pane.acceptPicker(item(t, "0"))
	assert.Equal(t, "other.go", h.pane.rel)
	assert.Equal(t, 1, h.pane.line)

	h.pane.acceptPicker(item(t, "7")) // out of range is ignored, not a panic
	assert.Equal(t, "other.go", h.pane.rel)
}

func TestAcceptPickerOutlineMovesWithinFile(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\ntwo\nthree\n"})
	h.pane.Open("a.go")
	h.pane.outline = []lsp.Symbol{{Name: "three", Kind: "func", Line: 3}}

	h.pane.acceptPicker(item(t, "0"))
	assert.Equal(t, "a.go", h.pane.rel, "an outline entry stays in the same file")
	assert.Equal(t, 2, h.pane.line)
}

func TestPickerHandlesKeysWhileOpen(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\ntwo\n"})
	h.pane.Open("a.go")
	h.pane.navHits = []lsp.Hit{{Path: "a.go", Line: 1}, {Path: "a.go", Line: 2}}
	h.pane.land(lspQuery{id: h.pane.req}, "references", h.pane.navHits, nil)
	h.draw()
	require.True(t, h.pane.picker.Open)

	// Keys go to the picker, not to the pane: the cursor must not move.
	h.key(t, 'j')
	assert.Equal(t, 0, h.pane.line)
}

func TestReloadRereadsFile(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\n"})
	h.pane.Open("a.go")
	require.NoError(t, os.WriteFile(filepath.Join(h.pane.cwd, "a.go"), []byte("one\ntwo\n"), 0o600))

	assert.Equal(t, "reloaded", h.pane.reload())
	assert.Len(t, h.pane.lines, 2)
}

func TestHelpModalWhileOpen(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\n"})
	h.pane.Open("a.go")
	h.key(t, '?')
	require.True(t, h.pane.help)

	surf := h.draw()
	require.Len(t, surf.Children, 1)
	assert.Contains(t, components.SurfaceText(surf.Children[0].Surface), "def")

	h.key(t, '?') // any key dismisses the modal
	assert.False(t, h.pane.help)
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

func TestPaintHoverCard(t *testing.T) {
	th := components.DefaultTheme()
	ctx := components.DrawContext{
		Max:    components.Size{Width: 80, Height: 24},
		Method: xui.WidthUnicode,
	}
	sub, ok := paintCard(ctx, th, &hoverCard{
		title:     "a.go:3",
		signature: "func main()",
		doc:       "Does the thing.",
	})
	require.True(t, ok)
	text := components.SurfaceText(sub.Surface)
	assert.Contains(t, text, "func main()")
	assert.Contains(t, text, "Does the thing.")
	assert.Contains(t, text, "a.go:3")
}

func TestPaintHoverCardSkippedWhenTooSmall(t *testing.T) {
	_, ok := paintCard(components.DrawContext{
		Max:    components.Size{Width: 10, Height: 4},
		Method: xui.WidthUnicode,
	}, components.DefaultTheme(), &hoverCard{signature: "x"})
	assert.False(t, ok)
}

func TestHoverCardClosesOnEscape(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\n"})
	h.pane.Open("a.go")
	h.pane.card = &hoverCard{title: "a.go:1", signature: "func f()"}

	h.code(t, xui.KeyEscape)
	assert.Nil(t, h.pane.card)
	assert.True(t, h.pane.Active(), "escape closes the card first, not the pane")
}

// ------------------------------------------------------------ helpers

func item(t *testing.T, id string) listpicker.Item {
	t.Helper()
	return listpicker.Item{ID: id}
}

func TestUTF16ColCountsSurrogates(t *testing.T) {
	line := "a😀b"
	assert.Equal(t, 0, utf16Col(line, 0))
	assert.Equal(t, 1, utf16Col(line, 1))
	assert.Equal(t, 3, utf16Col(line, len(line)-1), "an astral rune is two UTF-16 units")
	assert.Equal(t, len(utf16.Encode([]rune(line))), utf16Col(line, 99))
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
	// The fallback feeds titles and the clipboard, so it renders with forward
	// slashes too, not the os-native form.
	assert.Equal(t, filepath.ToSlash(outside), relPath(cwd, outside))
}

func TestHumanBytes(t *testing.T) {
	assert.Equal(t, "512 B", humanBytes(512))
	assert.Equal(t, "1.0 KB", humanBytes(1024))
	assert.Equal(t, "8.0 MB", humanBytes(maxFileBytes))
}

// A jump must land on the code, not in the indentation: asking a language
// server about column 0 of an indented line answers nothing.
func TestJumpLandsOnFirstNonBlank(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "package main\n\n\t\tfunc deep() {}\n"})
	h.pane.OpenAt("a.go", 3)
	assert.Equal(t, 2, h.pane.col)

	h.pane.Open("a.go")
	h.key(t, '/')
	for _, r := range "deep" {
		ctx := &components.EventContext{}
		h.pane.Handle(ctx, xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: r})
	}
	h.code(t, xui.KeyEnter)
	assert.Equal(t, 2, h.pane.line)
	assert.Equal(t, 2, h.pane.col, "a search hit lands on the code too")
}

func TestFirstNonBlank(t *testing.T) {
	assert.Equal(t, 0, firstNonBlank(""))
	assert.Equal(t, 0, firstNonBlank("func f()"))
	assert.Equal(t, 3, firstNonBlank("   "), "an all-blank line parks at its end")
	assert.Equal(t, 2, firstNonBlank("  x"))
	assert.Equal(t, 3, firstNonBlank(" \t\tx"))
	assert.Equal(t, 2, firstNonBlank("  日"))
}

// The query has to be visible while it is typed, along with the match count.
func TestSearchQueryIsVisibleInStatus(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "alpha\nbeta\nalpha\n"})
	h.pane.Open("a.go")

	h.key(t, '/')
	assert.Contains(t, components.SurfaceText(h.draw()), "/")

	for _, r := range "alpha" {
		ctx := &components.EventContext{}
		h.pane.Handle(ctx, xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: r})
	}
	text := components.SurfaceText(h.draw())
	assert.Contains(t, text, "/alpha")
	assert.Contains(t, text, "1/2")

	h.code(t, xui.KeyEnter) // leave search mode, keeping the query for n/N
	require.False(t, h.pane.searchMode)
	h.key(t, 'n')
	assert.Contains(t, components.SurfaceText(h.draw()), "2/2")

	h.key(t, '/') // a query with no matches says so instead of staying blank
	for _, r := range "zz" {
		ctx := &components.EventContext{}
		h.pane.Handle(ctx, xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: r})
	}
	assert.Contains(t, components.SurfaceText(h.draw()), "no matches")
}

func TestGGResetsBothAxes(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\ntwo\nthree\n"})
	h.pane.Open("a.go")
	h.pane.viewW = 20
	h.key(t, 'l')
	h.key(t, 'l')
	require.NotZero(t, h.pane.xScroll)

	h.key(t, 'G')
	assert.Equal(t, 2, h.pane.line)
	h.key(t, 'g')
	h.key(t, 'g')
	assert.Equal(t, 0, h.pane.line)
	assert.Equal(t, 0, h.pane.xScroll, "gg returns to the origin on both axes")
}

// A slow server must not be able to move the user: an answer that belongs to a
// file they have already left is dropped.
func TestStaleAnswerIsDropped(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\n", "b.go": "two\n", "target.go": "x\ny\nz\n"})
	h.pane.Open("a.go")

	h.pane.mu.Lock()
	q := lspQuery{abs: h.pane.abs, rel: h.pane.rel, line: 1, id: h.pane.req}
	h.pane.busy = true
	h.pane.mu.Unlock()

	h.pane.Open("b.go") // the user moves on while the server is thinking

	h.pane.land(q, "definition", []lsp.Hit{{Path: "target.go", Line: 3}}, nil)
	assert.Equal(t, "b.go", h.pane.rel, "the abandoned answer must not open its target")
	assert.False(t, h.pane.pushPicker)
	assert.False(t, h.pane.busy)
}

// A stale hover must not pop a card over the file the user is reading now.
func TestStaleOutlineIsDropped(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\n", "b.go": "two\n"})
	h.pane.Open("a.go")

	h.pane.mu.Lock()
	stale := h.pane.req
	h.pane.mu.Unlock()
	h.pane.Open("b.go")

	h.pane.mu.Lock()
	h.pane.outline = nil
	h.pane.mu.Unlock()

	staleQuery := lspQuery{abs: h.pane.resolve("a.go"), rel: "a.go", id: stale}
	h.pane.land(staleQuery, "outline", []lsp.Hit{{Path: "a.go", Line: 1}}, nil)
	assert.Nil(t, h.pane.outline)
}

// Search hits belong to the file they were found in.
func TestJumpClearsPreviousSearch(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\ntwo\n", "b.go": "xx\n"})
	h.pane.Open("a.go")
	h.pane.matches = []int{1}

	require.NoError(t, h.pane.load("b.go", 1))
	assert.Nil(t, h.pane.matches)
}

// The viewer answers to the same deny list as the tool gate.
func TestRefusesSensitivePath(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	h := newHarness(t, nil)
	h.pane.Open(filepath.Join(home, ".ssh", "id_rsa"))
	assert.Contains(t, h.pane.loadErr, "sensitive")
}

// A copy must not run the clipboard callback while the pane mutex is held.
func TestCopyCallbackRunsOutsideTheLock(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "one\n"})
	h.pane.Open("a.go")
	h.pane.onCopy = func(string) bool {
		assert.True(t, h.pane.mu.TryLock(), "the pane mutex must be free during the callback")
		h.pane.mu.Unlock()
		return true
	}

	ctx := &components.EventContext{}
	h.pane.Handle(ctx, xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: 'y'})
	assert.Contains(t, h.toasts, "copied")
}

// wakeAsync used to take the mutex it already held (called from
// handleKeyLocked), which deadlocked the UI thread on every LSP query.
// Regression test: wake must fire outside the pane mutex.
func TestHoverWakeRunsOutsideTheLock(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "package main\nfunc main() {}\n"})
	h.pane.mgr = lsp.New(t.TempDir(), false) // non-nil but inert; hover returns errNoServer
	t.Cleanup(h.pane.mgr.Close)

	woke := make(chan struct{}, 4)
	h.pane.onWake = func() {
		// A repaint triggered from the key path must not fire while p.mu is held.
		if h.pane.mu.TryLock() {
			h.pane.mu.Unlock()
			select {
			case woke <- struct{}{}:
			default:
			}
		}
	}

	h.pane.Open("a.go") // position() requires an open file

	done := make(chan struct{})
	go func() {
		h.key(t, 'K')
		close(done)
	}()

	select {
	case <-woke:
	case <-time.After(3 * time.Second):
		t.Fatal("hover wake deadlocked: wakeAsync took the mutex handleKeyLocked already holds")
	}
	<-done
}
