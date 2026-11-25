package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/app"
	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/tui/commands"
	"github.com/pulseaiclub/phi/internal/tui/composer"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/util/filesearch"
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

func TestOverlayCancellationRoutes(t *testing.T) {
	for _, route := range []string{"escape", "cancel"} {
		t.Run(route, func(t *testing.T) {
			e := newTestEditor(t)
			e.Update(controller.OverlayMsg{Kind: controller.OverlayErrorAsk, ErrorText: "failed"})
			require.True(t, e.submitter.StreamActive())

			if route == "escape" {
				e.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyEscape, Press: true})
			} else {
				e.Update(controller.CancelStreamMsg{})
			}
			require.False(t, e.overlays.Active())
			if route == "cancel" {
				require.Equal(t, controller.ActivityCancelled, e.activity.Current)
			}
			require.False(t, e.submitter.StreamActive())
		})
	}
}

func TestSpinnerAdvancesEveryFourthFrame(t *testing.T) {
	e := newTestEditor(t)
	e.activity.Apply(controller.ActivityStreaming)

	for range spinnerFrameDivisor - 1 {
		frame(t, e)
	}
	require.Equal(t, 0, e.spin.Frame)

	frame(t, e)
	require.Equal(t, 1, e.spin.Frame)

	e.activity.Apply(controller.ActivityCancelled)
	require.Equal(t, 0, e.spin.Frame, "activity changes reset the frame")
	for range spinnerFrameDivisor {
		frame(t, e)
	}
	require.Equal(t, 0, e.spin.Frame, "activities without spinner must not advance frames")
}

func TestActivityChangeRestartsSpinnerPace(t *testing.T) {
	e := newTestEditor(t)
	e.activity.Apply(controller.ActivityStreaming)
	for range 2 {
		e.advanceSpinner()
	}

	e.activity.Apply(controller.ActivityTools)
	require.Equal(t, 0, e.spin.Frame)
	require.Equal(t, 0, e.spinTick)

	for range spinnerFrameDivisor {
		e.advanceSpinner()
	}
	require.Equal(t, 1, e.spin.Frame)
}

func diffEditor(t *testing.T, dir string) *Editor {
	t.Helper()
	e := NewEditor(app.NewApp(nil), controller.NewBus(nil), &controller.EngineController{}, nil,
		components.DefaultTheme(), dir, "", nil, nil)
	// Keep the real focus/blocking wiring without a terminal redraw callback.
	e.composer.Wire(composer.WireDeps{
		Transcript:            e.transcript,
		Submitter:             e.submitter,
		Commands:              e.commands,
		Bus:                   e.bus,
		DrainBus:              e.drainBus,
		OverlayBlocksComposer: e.blocksComposer,
		RequestFocusEditor:    func() { e.App.RequestFocus(e) },
		RequestFocus:          e.App.RequestFocus,
	})
	return e
}

func diffKey(e *Editor, code xui.KeyCode, r rune) *components.EventContext {
	ctx := &components.EventContext{}
	e.Handle(ctx, xui.KeyEvent{Press: true, Code: code, Rune: r})
	return ctx
}

func writeDiffFile(t *testing.T, dir, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, path), []byte(content), 0o600))
}

func TestDiffPickerCombinesFilesAndReturnsToSelection(t *testing.T) {
	dir := gitFixtureRepo(t)
	writeDiffFile(t, dir, "both.txt", "base\n")
	gitFixtureRun(t, dir, "add", ".")
	gitFixtureRun(t, dir, "commit", "-m", "base")
	writeDiffFile(t, dir, "both.txt", "staged-line\n")
	gitFixtureRun(t, dir, "add", ".")
	writeDiffFile(t, dir, "both.txt", "worktree-line\n")
	writeDiffFile(t, dir, "new.txt", "untracked-line\n")
	e := diffEditor(t, dir)

	require.True(t, e.commands.DispatchSlash("/diff", commands.NewContext(e.bus, nil)))
	assert.False(t, e.diff.Active())
	text := branchOverlay(t, e)
	assert.Contains(t, text, "Changes")
	assert.NotContains(t, text, "Staged")
	assert.NotContains(t, text, "Unstaged")
	assert.Equal(t, 1, strings.Count(text, "both.txt"))
	assert.NotContains(t, text, "Untracked")
	assert.Contains(t, text, "New file")
	for _, r := range "both" {
		diffKey(e, xui.KeyRune, r)
	}
	ctx := diffKey(e, xui.KeyEnter, 0)
	assert.Same(t, e, ctx.Focus, "the shared picker must hand focus to the detail view")
	require.True(t, e.diff.Active())
	text = components.SurfaceText(frame(t, e))
	assert.Contains(t, text, "base")
	assert.Contains(t, text, "worktree-line")
	assert.NotContains(t, text, "staged-line")
	diffKey(e, xui.KeyEscape, 0)
	assert.False(t, e.diff.Active())
	assert.Equal(t, "both", e.composer.ListState().Query)
	assert.Contains(t, e.composer.ListState().SelectedID, "both.txt")
	diffKey(e, xui.KeyEscape, 0)
	_, open := e.composer.ListOverlay(components.DrawContext{})
	assert.False(t, open)
	assert.False(t, e.blocksComposer())
}

func TestDiffPickerEmptyAndGitError(t *testing.T) {
	for _, tc := range []struct {
		name string
		dir  string
		want string
		kind toast.ToastKind
	}{
		{"clean", gitFixtureRepo(t), "No changes", toast.ToastSuccess},
		{"not repository", t.TempDir(), "not a git repository", toast.ToastError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := diffEditor(t, tc.dir)
			e.OpenDiff(nil)
			assert.False(t, e.diff.Active())
			_, open := e.composer.ListOverlay(components.DrawContext{})
			assert.False(t, open)
			assert.Contains(t, e.toast.Message, tc.want)
			assert.Equal(t, tc.kind, e.toast.Kind)
		})
	}
}

func TestDiffPickerRefreshAndExplicitCommand(t *testing.T) {
	dir := gitFixtureRepo(t)
	writeDiffFile(t, dir, "new.txt", "new content\n")
	e := diffEditor(t, dir)
	e.OpenDiff(nil)
	diffKey(e, xui.KeyEnter, 0)
	assert.Contains(t, components.SurfaceText(frame(t, e)), "new content")
	gitFixtureRun(t, dir, "add", ".")
	gitFixtureRun(t, dir, "commit", "-m", "clean")
	diffKey(e, xui.KeyEscape, 0)
	assert.Equal(t, "No changes", e.toast.Message)
	assert.False(t, e.diff.Active())

	writeDiffFile(t, dir, "new.txt", "staged content\n")
	gitFixtureRun(t, dir, "add", ".")
	e.OpenDiff([]string{"staged"})
	require.True(t, e.diff.Active())
	assert.Contains(t, components.SurfaceText(frame(t, e)), "staged content")
	diffKey(e, xui.KeyEscape, 0)
	_, open := e.composer.ListOverlay(components.DrawContext{})
	assert.False(t, open, "explicit commands still close to chat")
}

func TestDiffPickerClearsBranchDeleteHandler(t *testing.T) {
	dir := gitFixtureRepo(t)
	writeDiffFile(t, dir, "new.txt", "content\n")
	e := diffEditor(t, dir)
	require.True(t, e.commands.DispatchSlash("/branch", commands.NewContext(e.bus, nil)))
	e.OpenDiff(nil)
	e.Handle(&components.EventContext{}, xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: 'x', Mods: xui.ModCtrl})
	assert.NotContains(t, branchOverlay(t, e), "Delete")
	assert.Contains(t, branchOverlay(t, e), "new.txt")
}

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
	assert.False(t, e.code.Active())
	text := strings.Join(strings.Fields(branchOverlay(t, e)), " ")
	assert.Contains(t, text, "Files")
	assert.Contains(t, text, "a.go .")
	assert.Contains(t, text, "a.go internal")
	for _, r := range "internal/a.go" {
		diffKey(e, xui.KeyRune, r)
	}
	ctx := diffKey(e, xui.KeyEnter, 0)
	assert.Same(t, e, ctx.Focus)
	require.True(t, e.code.Active())
	assert.Contains(t, components.SurfaceText(frame(t, e)), "nested file")
	diffKey(e, xui.KeyEscape, 0)
	assert.False(t, e.code.Active())
	assert.False(t, e.blocksComposer())
}

func TestCodePickerEmptyAndCancel(t *testing.T) {
	if _, err := filesearch.ResolveFD(); err != nil {
		t.Skip(err)
	}
	e := diffEditor(t, t.TempDir())
	e.diff.OpenText("", nil)
	e.OpenCode(nil)
	assert.False(t, e.diff.Active())
	assert.Contains(t, branchOverlay(t, e), "No files found")
	diffKey(e, xui.KeyEscape, 0)
	_, open := e.composer.ListOverlay(components.DrawContext{})
	assert.False(t, open)
	assert.False(t, e.code.Active())
}

func TestCodePickerOpensLiteralPath(t *testing.T) {
	if _, err := filesearch.ResolveFD(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	writeDiffFile(t, dir, "@file with spaces.go", "literal path\n")
	e := diffEditor(t, dir)
	e.OpenCode(nil)
	diffKey(e, xui.KeyEnter, 0)
	require.True(t, e.code.Active())
	assert.Contains(t, components.SurfaceText(frame(t, e)), "literal path")
}

func TestCodePickerSearchError(t *testing.T) {
	if _, err := filesearch.ResolveFD(); err != nil {
		t.Skip(err)
	}
	e := diffEditor(t, filepath.Join(t.TempDir(), "missing"))
	e.OpenCode(nil)
	assert.Contains(t, e.toast.Message, "List files:")
	_, open := e.composer.ListOverlay(components.DrawContext{})
	assert.False(t, open)
	assert.False(t, e.code.Active())
}

func TestCodePaneTakesKeysAndReturnsFocus(t *testing.T) {
	e := newTestEditor(t)
	e.OpenCode([]string{"a.go"})
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
	e.OpenCode([]string{"a.go"})

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

	e.OpenCode([]string{"a.go"})
	assert.False(t, e.diff.Active())
	assert.True(t, e.code.Active())
}
