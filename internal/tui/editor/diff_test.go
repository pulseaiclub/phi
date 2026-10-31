package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/app"
	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/tui/commands"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func diffEditor(t *testing.T, dir string) *Editor {
	t.Helper()
	e := NewEditor(app.NewApp(nil), controller.NewBus(nil), &controller.EngineController{}, nil,
		components.DefaultTheme(), dir, "", nil, 0, nil)
	// Keep the real focus/blocking wiring without a terminal redraw callback.
	e.composer.Wire(e.transcript, e.submitter, e.commands, dir, e.bus, e.drainBus, nil, nil,
		e.blocksComposer, nil, nil, nil, nil, func() { e.App.RequestFocus(e) }, e.App.RequestFocus, nil)
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
			e.openDiff(nil)
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
	e.openDiff(nil)
	diffKey(e, xui.KeyEnter, 0)
	assert.Contains(t, components.SurfaceText(frame(t, e)), "new content")
	gitFixtureRun(t, dir, "add", ".")
	gitFixtureRun(t, dir, "commit", "-m", "clean")
	diffKey(e, xui.KeyEscape, 0)
	assert.Equal(t, "No changes", e.toast.Message)
	assert.False(t, e.diff.Active())

	writeDiffFile(t, dir, "new.txt", "staged content\n")
	gitFixtureRun(t, dir, "add", ".")
	e.openDiff([]string{"staged"})
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
	e.openDiff(nil)
	e.Handle(&components.EventContext{}, xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: 'x', Mods: xui.ModCtrl})
	assert.NotContains(t, branchOverlay(t, e), "Delete")
	assert.Contains(t, branchOverlay(t, e), "new.txt")
}
