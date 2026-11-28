package editor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/app"
	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/tui/commands"
	"github.com/pulseaiclub/phi/internal/tui/composer"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/util/filesearch"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	domain "github.com/pulseaiclub/phi/internal/core"
	"github.com/pulseaiclub/phi/internal/extension/core"
	"github.com/pulseaiclub/phi/internal/extension/loader"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/session"
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

func TestConfirmActivityExpiry(t *testing.T) {
	for _, action := range []string{"reply", "dismiss"} {
		t.Run(action, func(t *testing.T) {
			e := newTestEditor(t)
			e.activity.Apply(controller.ActivityCancelled)
			e.Update(controller.OverlayMsg{Kind: controller.OverlayExtConfirm})
			e.Update(controller.FooterMsg{
				Kind: controller.FooterClearIfActivity, If: controller.ActivityCancelled,
			})
			require.Equal(t, controller.ActivityAwaitingApproval, e.activity.Current)
			if action == "reply" {
				e.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
			} else {
				e.Update(controller.OverlayMsg{Kind: controller.OverlayExtConfirmDismiss})
			}
			require.Equal(t, controller.ActivityIdle, e.activity.Current)
		})
	}
}

func TestOverlayCancellationRoutes(t *testing.T) {
	for _, kind := range []struct {
		name string
		kind controller.OverlayKind
	}{
		{"confirm", controller.OverlayExtConfirm},
		{"error", controller.OverlayErrorAsk},
	} {
		for _, route := range []string{"escape", "cancel"} {
			t.Run(kind.name+"/"+route, func(t *testing.T) {
				e := newTestEditor(t)
				confirmReply := make(chan controller.ExtConfirmReply, 1)
				e.Update(controller.OverlayMsg{
					Kind: kind.kind, ConfirmReply: confirmReply, ErrorText: "failed",
				})
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
				switch kind.kind {
				case controller.OverlayExtConfirm:
					require.Len(t, confirmReply, 1, "cancellation must unblock the confirmation waiter")
					require.Equal(t, controller.ExtConfirmReply{}, <-confirmReply)
				case controller.OverlayErrorAsk:
					require.False(t, e.submitter.StreamActive())
				}
			})
		}
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

// TestEditorAppliesBranchLabel removed: it constructed an Editor with a nil vx
// and only passed because Update nil-guarded vx before QueueRefresh. Guard is
// gone — a nil vx now panics, which is the intended contract.
// TestBranchSlashOpensPickerOverRealRepo walks the whole path the way the shell
// wires it: registry → BranchCommands → git → branchlist → composer overlay.
func TestBranchSlashOpensPickerOverRealRepo(t *testing.T) {
	e := branchEditor(t)

	require.True(t, e.commands.DispatchSlash("/branch", commands.NewContext(e.bus, nil)))

	text := branchOverlay(t, e)
	assert.Contains(t, text, "Branches")
	assert.Contains(t, text, "main")
	assert.NotContains(t, text, "current", "two columns only — no badge column")
}

// TestBranchPickerCtrlXDeletesOverRealRepo mirrors the /sessions delete: the
// picker arms on ctrl+x, Y confirms, git drops the branch, and the open list
// refreshes in place.
func TestBranchPickerCtrlXDeletesOverRealRepo(t *testing.T) {
	e := branchEditor(t)
	gitFixtureRun(t, e.cwd, "branch", "old-work")
	require.True(t, e.commands.DispatchSlash("/branch", commands.NewContext(e.bus, nil)))
	require.Contains(t, branchOverlay(t, e), "old-work")

	key := func(code xui.KeyCode, r rune, mods xui.Modifiers) {
		e.composer.Handle(&components.EventContext{}, xui.KeyEvent{Press: true, Code: code, Rune: r, Mods: mods})
	}
	key(xui.KeyDown, 0, 0) // current branch anchors the list, so old-work is second
	key(xui.KeyRune, 'x', xui.ModCtrl)
	key(xui.KeyRune, 'y', 0)

	cmd := exec.CommandContext(t.Context(), "git", "branch", "--list", "old-work")
	cmd.Dir = e.cwd
	out, err := cmd.Output()
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(string(out)), "git must have dropped the branch")
	assert.NotContains(t, branchOverlay(t, e), "old-work", "the open picker refreshed its rows")
	assert.Contains(t, branchOverlay(t, e), "main")
}

func TestBranchPickerDeletesUnmergedBranchAfterOneConfirmation(t *testing.T) {
	for _, accept := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "delete"}[accept], func(t *testing.T) {
			e := branchEditor(t)
			gitFixtureRun(t, e.cwd, "checkout", "-b", "feature")
			require.NoError(t, os.WriteFile(filepath.Join(e.cwd, "feature.txt"), []byte("work\n"), 0o600))
			gitFixtureRun(t, e.cwd, "add", ".")
			gitFixtureRun(t, e.cwd, "commit", "-m", "unmerged work")
			gitFixtureRun(t, e.cwd, "checkout", "main")
			require.True(t, e.commands.DispatchSlash("/branch", commands.NewContext(e.bus, nil)))
			key := func(code xui.KeyCode, r rune, mods xui.Modifiers) {
				e.composer.Handle(
					&components.EventContext{},
					xui.KeyEvent{Press: true, Code: code, Rune: r, Mods: mods},
				)
			}
			key(xui.KeyDown, 0, 0)
			key(xui.KeyRune, 'x', xui.ModCtrl)
			if accept {
				key(xui.KeyRune, 'y', 0)
			} else {
				key(xui.KeyRune, 'n', 0)
			}
			for _, msg := range e.bus.Drain() {
				_, isOverlay := msg.(controller.OverlayMsg)
				assert.False(t, isOverlay, "deletion must not ask for a second confirmation")
			}
			cmd := exec.CommandContext(t.Context(), "git", "branch", "--list", "feature")
			cmd.Dir = e.cwd
			out, err := cmd.Output()
			require.NoError(t, err)
			if accept {
				assert.Empty(t, strings.TrimSpace(string(out)))
				assert.NotContains(t, branchOverlay(t, e), "feature")
			} else {
				assert.Contains(t, string(out), "feature")
				assert.Contains(t, branchOverlay(t, e), "feature")
			}
		})
	}
}

// branchEditor builds the shell wiring the editor uses, over a real repo.
func branchEditor(t *testing.T) *Editor {
	t.Helper()
	dir := gitFixtureRepo(t)
	bus := controller.NewBus(nil)
	e := &Editor{
		composer: composer.NewComposerPane(components.DefaultTheme(), "m", dir),
		bus:      bus,
		cwd:      dir,
	}
	e.composer.Wire(composer.WireDeps{})

	registry := commands.NewCommandRegistry()
	builtins := commands.NewBuiltinRegistry(
		registry,
		commands.NewContext(bus, nil),
		bus,
		nil,
		e.composer,
		nil,
		dir,
		nil,
		nil,
		e,
		nil,
	)
	e.commands = builtins.Registry

	return e
}

func branchOverlay(t *testing.T, e *Editor) string {
	t.Helper()
	overlay, ok := e.composer.ListOverlay(components.DrawContext{
		Max:    components.Size{Width: 100, Height: 30},
		Method: xui.WidthUnicode,
	})
	require.True(t, ok, "/branch must open the picker")
	return components.SurfaceText(overlay.Surface)
}

func gitFixtureRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	gitFixtureRun(t, dir, "init", "-b", "main")
	gitFixtureRun(t, dir, "config", "user.email", "phi@example.com")
	gitFixtureRun(t, dir, "config", "user.name", "phi tests")
	gitFixtureRun(t, dir, "commit", "--allow-empty", "-m", "init")
	return dir
}

func gitFixtureRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=phi tests", "GIT_AUTHOR_EMAIL=phi@example.com",
		"GIT_COMMITTER_NAME=phi tests", "GIT_COMMITTER_EMAIL=phi@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

func TestLoopFailureEndsStreamingBeforeDismiss(t *testing.T) {
	e := newTestEditor(t)
	e.bus.Publish(controller.SessionEventMsg{Event: session.AssistantMessageUpdate{Message: session.Message{
		ID: "partial", State: session.StateStreaming, Text: "partial output",
	}}})
	e.bus.Publish(controller.LoopFinishedMsg{ErrorText: "stream closed without assistant output"})
	e.drainBus()
	require.False(t, e.transcript.IsStreaming())
	require.True(t, e.overlays.Active())
	require.True(t, e.submitter.StreamActive(), "dialog still blocks session switches")
	e.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyEscape, Press: true})
	require.False(t, e.overlays.Active())
	require.False(t, e.submitter.IsBusy(), "dismiss releases the composer")
	require.Equal(t, "partial output", e.transcript.Snapshot().Messages[0].Text)
}

func TestCancellationDropsQueuedLoopFailure(t *testing.T) {
	e := newTestEditor(t)
	e.bus.Publish(controller.LoopFinishedMsg{ErrorText: "context canceled"})
	e.ctrl.Cancel()
	e.drainBus()
	require.False(t, e.overlays.Active())
	require.False(t, e.submitter.IsBusy())
}

func TestCancelledCompletionDoesNotShowError(t *testing.T) {
	e := newTestEditor(t)
	e.transcript.ApplySession(session.AssistantMessageUpdate{Message: session.Message{
		ID: "partial", State: session.StateStreaming, Text: "partial output",
	}})
	e.Update(controller.LoopFinishedMsg{Cancelled: true})
	require.False(t, e.overlays.Active())
	require.False(t, e.transcript.IsStreaming())
	require.Equal(t, controller.ActivityCancelled, e.activity.Current)
}

func TestRetryUnavailableRestoresIdle(t *testing.T) {
	e := newTestEditor(t)
	e.Update(controller.OverlayMsg{Kind: controller.OverlayErrorAsk, ErrorText: "error"})
	e.Handle(&components.EventContext{}, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
	require.False(t, e.overlays.Active())
	require.False(t, e.submitter.IsBusy())
	require.Equal(t, controller.ActivityIdle, e.activity.Current)
}

func TestCompactionResultToast(t *testing.T) {
	tests := []struct {
		name    string
		result  controller.CompactionFinishedMsg
		message string
		kind    toast.ToastKind
	}{
		{
			name: "committed", result: controller.CompactionFinishedMsg{Compacted: true},
			message: "Compacted context", kind: toast.ToastSuccess,
		},
		{
			name:    "committed with tokens",
			result:  controller.CompactionFinishedMsg{Compacted: true, TokensBefore: 12000},
			message: "Compacted context (was 12k tokens)", kind: toast.ToastSuccess,
		},
		{
			name:    "committed despite a late cancel",
			result:  controller.CompactionFinishedMsg{Compacted: true, Cancelled: true, TokensBefore: 2048},
			message: "Compacted context (was 2.0k tokens)", kind: toast.ToastSuccess,
		},
		{
			name:    "cancelled",
			result:  controller.CompactionFinishedMsg{Err: context.Canceled, Cancelled: true},
			message: "Compaction cancelled", kind: toast.ToastWarning,
		},
		{
			name: "cancelled by context alone", result: controller.CompactionFinishedMsg{Cancelled: true},
			message: "Compaction cancelled", kind: toast.ToastWarning,
		},
		{
			name: "failed", result: controller.CompactionFinishedMsg{Err: errors.New("boom")},
			message: "Compaction failed: boom", kind: toast.ToastError,
		},
		{
			name: "nothing to do", result: controller.CompactionFinishedMsg{},
			message: "Nothing to compact yet", kind: toast.ToastWarning,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTestEditor(t)
			e.Update(tt.result)
			assert.Equal(t, tt.message, e.toast.Message)
			assert.Equal(t, tt.kind, e.toast.Kind)
			assert.True(t, e.toast.Visible())
		})
	}
}

// forkEditor wires the shell the way cmd does, on a live controller over a
// throwaway project, so /fork runs the whole path: dispatch → controller fork →
// transcript refresh.
func forkEditor(t *testing.T) *Editor {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, key := range []string{"PHI_MODEL", "PHI_API_KEY", "PHI_BASE_URL", "PHI_THINK_LEVEL"} {
		t.Setenv(key, "")
	}
	t.Setenv(loader.EnvExtensions, "off")
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".phi"), 0o700))
	config := `models:
  - name: primary
    default: true
    api: OpenAI
    api_key: test-key
    base_url: "http://127.0.0.1:9"
`
	require.NoError(t, os.WriteFile(filepath.Join(home, ".phi", "config.yaml"), []byte(config), 0o600))

	dir := t.TempDir()
	proj, err := project.Discover(dir)
	require.NoError(t, err)
	bus := controller.NewBus(nil)
	ctrl, err := controller.NewController(bus, proj)
	require.NoError(t, err)
	t.Cleanup(ctrl.Close)

	return NewEditor(app.NewApp(nil), bus, ctrl, nil, components.DefaultTheme(), dir, "primary", nil, nil)
}

func TestQueuedExtensionPromptStaysWithRunnerAndSession(t *testing.T) {
	e := forkEditor(t)
	api := ext.NewAPI()
	f := core.NewFiber("sender", loader.Discovered{})
	f.Attach(api, core.FiberActive)
	e.ctrl.Extensions().(*core.Runner).AddFiberInitial(f)

	checkDelivery := func(wantStart bool) {
		t.Helper()
		e.drainBus()
		started := false
		for _, msg := range e.bus.Drain() {
			if _, ok := msg.(controller.LoopStartedMsg); ok {
				started = true
			}
		}
		e.ctrl.Cancel()
		assert.Equal(t, wantStart, started)
	}
	api.SendUserMessage("outgoing session")
	require.NoError(t, e.ctrl.NewSession())
	checkDelivery(false)
	api.SendUserMessage("new session")
	checkDelivery(true)
	_, err := e.ctrl.ForkSession()
	require.NoError(t, err)
	api.SendUserMessage("forked session")
	checkDelivery(true)
	api.SendUserMessage("outgoing runner, same session")
	_, _, err = e.ctrl.ReloadExtensions()
	require.NoError(t, err)
	checkDelivery(false)
}

func TestForkSlashSwitchesToANewSession(t *testing.T) {
	e := forkEditor(t)
	srcID, srcFile := e.ctrl.SessionID(), e.ctrl.SessionFile()

	require.True(t, e.commands.DispatchSlash("/fork", commands.NewContext(e.bus, nil)))
	e.drainBus()

	assert.NotEqual(t, srcID, e.ctrl.SessionID(), "/fork must open the session it created")
	assert.NotEqual(t, srcFile, e.ctrl.SessionFile())
	assert.Empty(t, e.transcript.Snapshot().Messages, "a fresh session forks empty")
}

func TestForkSlashRejectsArguments(t *testing.T) {
	e := forkEditor(t)
	srcID := e.ctrl.SessionID()

	require.True(t, e.commands.DispatchSlash("/fork now", commands.NewContext(e.bus, nil)))
	e.drainBus()

	assert.Equal(t, srcID, e.ctrl.SessionID(), "arguments must not fork silently")
}

func TestPaletteReopenReflectsThinkingSelectionAndModelSwitch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, key := range []string{"PHI_MODEL", "PHI_API_KEY", "PHI_BASE_URL", "PHI_THINK_LEVEL"} {
		t.Setenv(key, "")
	}
	t.Setenv(loader.EnvExtensions, "off")
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".phi"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".phi", "config.yaml"), []byte(`models:
  - name: primary
    default: true
    api: OpenAI
    api_key: test-key
    base_url: http://127.0.0.1:9
    think_level: off
  - name: secondary
    api: OpenAI
    api_key: test-key
    base_url: http://127.0.0.1:9
    think_level: high
`), 0o600))
	cwd := t.TempDir()
	proj, err := project.Discover(cwd)
	require.NoError(t, err)
	bus := controller.NewBus(nil)
	ctrl, err := controller.NewController(bus, proj)
	require.NoError(t, err)
	t.Cleanup(ctrl.Close)
	e := NewEditor(
		app.NewApp(nil),
		bus,
		ctrl,
		nil,
		components.DefaultTheme(),
		cwd,
		"primary",
		nil,
		[]string{"primary", "secondary"},
	)

	key := func(code xui.KeyCode) {
		e.composer.Handle(&components.EventContext{}, xui.KeyEvent{Press: true, Code: code})
	}
	openSettings := func(query, title string) *palette.CommandPalette {
		t.Helper()
		e.composer.Handle(
			&components.EventContext{},
			xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: 'k', Mods: xui.ModCtrl},
		)
		for _, r := range query {
			e.composer.Handle(&components.EventContext{}, xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: r})
		}
		key(xui.KeyEnter)
		overlay, ok := e.composer.PaletteOverlay(
			components.DrawContext{Max: components.Size{Width: 100, Height: 30}, Method: xui.WidthUnicode},
		)
		require.True(t, ok)
		p, ok := overlay.Surface.Widget.(*palette.CommandPalette)
		require.True(t, ok)
		require.Equal(t, title, p.Title)
		return p
	}
	assertCurrent := func(p *palette.CommandPalette, current domain.ThinkMode) {
		t.Helper()
		for _, cmd := range p.Commands {
			level := cmd.ID[len("think-"):]
			assert.Equal(t, chrome.CurrentMarker(level == string(current))+level, cmd.Verb)
		}
	}

	p := openSettings("think", "Thinking Level")
	assertCurrent(p, domain.Off)
	key(xui.KeyDown) // minimal
	key(xui.KeyDown) // low
	key(xui.KeyEnter)
	require.False(t, p.Picker.Open)
	require.Equal(t, domain.Low, ctrl.ThinkLevel())
	p = openSettings("think", "Thinking Level")
	assertCurrent(p, domain.Low)
	e.composer.HidePalette()

	p = openSettings("settings model", "Select Model")
	require.Len(t, p.Commands, 2)
	assert.Equal(t, chrome.CurrentMarker(true)+"primary", p.Commands[0].Verb)
	assert.Equal(t, chrome.CurrentMarker(false)+"secondary", p.Commands[1].Verb)
	key(xui.KeyDown)
	key(xui.KeyEnter)
	require.False(t, p.Picker.Open)
	require.Equal(t, "secondary", ctrl.ModelName())

	p = openSettings("settings model", "Select Model")
	require.Len(t, p.Commands, 2)
	assert.Equal(t, chrome.CurrentMarker(false)+"primary", p.Commands[0].Verb)
	assert.Equal(t, chrome.CurrentMarker(true)+"secondary", p.Commands[1].Verb)
	e.composer.HidePalette()

	p = openSettings("think", "Thinking Level")
	assertCurrent(p, domain.High)
}

func TestPaletteRefreshesRegistryOnlyWhenOpening(t *testing.T) {
	e := newTestEditor(t)
	current := "first"
	builds := 0
	e.commands.Register(commands.Command{
		Name: "live-setting",
		Build: func(commands.Context) palette.PaletteCommand {
			builds++
			return palette.PaletteCommand{ID: "live-setting", Verb: current}
		},
	})
	// The composer rebuilds its own palette root on open; the last registered
	// command is live-setting, so its Verb shows which build ran.
	root := func() string {
		overlay, ok := e.composer.PaletteOverlay(components.DrawContext{
			Max:    components.Size{Width: 100, Height: 30},
			Method: xui.WidthUnicode,
		})
		require.True(t, ok, "palette must be open")
		p, ok := overlay.Surface.Widget.(*palette.CommandPalette)
		require.True(t, ok)
		return p.Root[len(p.Root)-1].Verb
	}
	toggle := func() {
		e.composer.Handle(
			&components.EventContext{},
			xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: 'k', Mods: xui.ModCtrl},
		)
	}
	toggle()
	require.Equal(t, 1, builds)
	assert.Equal(t, "first", root())
	toggle()
	assert.Equal(t, 1, builds, "closing must not rebuild commands before Hide restores parent frames")
	current = "second"
	toggle()
	require.Equal(t, 2, builds)
	assert.Equal(t, "second", root())
}

func TestModelPaletteSavesDefaultAndRestoresSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, key := range []string{"PHI_MODEL", "PHI_API_KEY", "PHI_BASE_URL", "PHI_THINK_LEVEL"} {
		t.Setenv(key, "")
	}
	t.Setenv(loader.EnvExtensions, "off")
	configDir := filepath.Join(home, ".phi")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	configPath := filepath.Join(configDir, "config.yaml")
	config := `models:
  - name: primary
    default: true
    api_key: test-key
    base_url: http://127.0.0.1:9
    context_window: 192000
  - name: secondary
    api_key: test-key
    base_url: http://127.0.0.1:9
    context_window: 1000000
`
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0o600))
	cwd := t.TempDir()
	proj, err := project.Discover(cwd)
	require.NoError(t, err)
	bus := controller.NewBus(nil)
	ctrl, err := controller.NewController(bus, proj)
	require.NoError(t, err)
	t.Cleanup(ctrl.Close)
	e := NewEditor(app.NewApp(nil), bus, ctrl, nil, components.DefaultTheme(), cwd,
		"primary", nil, []string{"primary", "secondary"})
	key := func(code xui.KeyCode, r rune, mods xui.Modifiers) {
		t.Helper()
		e.composer.Handle(&components.EventContext{}, xui.KeyEvent{Press: true, Code: code, Rune: r, Mods: mods})
	}
	openModel := func(query string) *palette.CommandPalette {
		t.Helper()
		key(xui.KeyRune, 'k', xui.ModCtrl)
		for _, r := range "settings model" {
			key(xui.KeyRune, r, 0)
		}
		key(xui.KeyEnter, 0, 0)
		for _, r := range query {
			key(xui.KeyRune, r, 0)
		}
		overlay, ok := e.composer.PaletteOverlay(components.DrawContext{
			Max: components.Size{Width: 100, Height: 30}, Method: xui.WidthUnicode,
		})
		require.True(t, ok)
		p, ok := overlay.Surface.Widget.(*palette.CommandPalette)
		require.True(t, ok)
		return p
	}

	p := openModel("secondary")
	key(xui.KeyEnter, 0, 0)
	require.False(t, p.Picker.Open)
	require.Equal(t, "secondary", ctrl.ModelName())
	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, config, string(data), "Enter must not modify global defaults")
	require.NoError(t, ctrl.SetModel("primary"))
	previous, err := session.NewSessionManager(cwd, session.WithSessionDir(ctrl.SessionDir()),
		session.WithShouldFlush(true), session.WithModel("primary"))
	require.NoError(t, err)
	_, err = previous.Append(domain.Message{Role: domain.RoleAssistant, Content: "done"})
	require.NoError(t, err)
	firstID := previous.ID()
	require.NoError(t, ctrl.NewSession())

	p = openModel("secondary")
	assert.Empty(t, p.Commands[1].Shortcut)
	// A failed write must keep the picker open and leave the active model alone.
	require.NoError(t, os.Mkdir(configPath+".bak", 0o700))
	key(xui.KeyRune, 's', xui.ModCtrl)
	require.True(t, p.Picker.Open)
	key(xui.KeyRune, 'y', 0)
	require.True(t, p.Picker.Open)
	require.Equal(t, "primary", ctrl.ModelName())
	require.NoError(t, os.Remove(configPath+".bak"))
	key(xui.KeyRune, 's', xui.ModCtrl)
	require.True(t, p.Picker.Open)
	require.Equal(t, "primary", ctrl.ModelName())
	data, err = os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, config, string(data), "Ctrl+S must wait for confirmation before writing")
	key(xui.KeyEnter, 0, 0)
	require.False(t, p.Picker.Open)
	require.Equal(t, "secondary", ctrl.ModelName())
	assert.Equal(t, "secondary", e.composer.Chat.TopRightLabel.Text)
	assert.Equal(t, 1000000, ctrl.ContextWindow())
	doc, err := project.ReadConfigDoc(configPath)
	require.NoError(t, err)
	assert.False(t, doc.Models[0].Default)
	assert.True(t, doc.Models[1].Default)
	require.NoError(t, ctrl.NewSession())
	assert.Equal(t, "secondary", ctrl.ModelName())

	ctrl.Close()
	restarted, err := controller.NewController(controller.NewBus(nil), proj)
	require.NoError(t, err)
	t.Cleanup(restarted.Close)
	assert.Equal(t, "secondary", restarted.ModelName(), "saved default must survive restart")
	_, err = restarted.Resume(firstID)
	require.NoError(t, err)
	assert.Equal(t, "primary", restarted.ModelName(), "session model must take precedence on resume")
}

// On a short terminal the transcript area can shrink to a few rows. The open
// palette must not follow it down to an empty box that still captures keys.
func TestPaletteStaysUsableOnShortTerminals(t *testing.T) {
	e := newTestEditor(t)
	e.composer.Handle(&components.EventContext{},
		xui.KeyEvent{Press: true, Code: xui.KeyRune, Rune: 'k', Mods: xui.ModCtrl})
	require.True(t, e.composer.PaletteOpen())

	root := e.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 8}, Method: xui.WidthUnicode})
	var overlay *components.Surface
	for i := range root.Children {
		if _, ok := root.Children[i].Surface.Widget.(*palette.CommandPalette); ok {
			overlay = &root.Children[i].Surface
		}
	}
	require.NotNil(t, overlay, "the palette overlay draws")
	require.Len(t, overlay.Children, 1)
	panel := overlay.Children[0].Surface
	assert.Equal(t, 7, panel.Size.Height, "one row plus full chrome, not an empty box")
	text := components.SurfaceText(panel)
	assert.Contains(t, text, chrome.FilterPrompt, "the filter line is visible")
	assert.Contains(t, text, "esc close", "the hint is visible")
}

// TestSkillSlashOpensPicker walks the wiring the way the shell uses it:
// registry → SkillsCommands → skills.LoadAll → listrows → composer overlay.
// /skill is a slash command, not a Ctrl+K palette entry.
func TestSkillSlashOpensPicker(t *testing.T) {
	e := skillEditor(t)

	require.True(t, e.commands.DispatchSlash("/skill", commands.NewContext(e.bus, nil)))

	text := skillOverlay(t, e)
	assert.Contains(t, text, "Skills")
	assert.Contains(t, text, "extract-and-distill")
}

// Accepting a row attaches the skill to the composer and refocuses chat.
func TestSkillPickerAcceptAttachesSkill(t *testing.T) {
	e := skillEditor(t)
	require.True(t, e.commands.DispatchSlash("/skill", commands.NewContext(e.bus, nil)))
	_, ok := e.composer.ListOverlay(components.DrawContext{
		Max:    components.Size{Width: 100, Height: 30},
		Method: xui.WidthUnicode,
	})
	require.True(t, ok, "/skill must have opened the picker")

	key := func(code xui.KeyCode, r rune, mods xui.Modifiers) {
		e.composer.Handle(&components.EventContext{}, xui.KeyEvent{Press: true, Code: code, Rune: r, Mods: mods})
	}
	key(xui.KeyEnter, 0, 0)

	assert.Equal(t, []string{"extract-and-distill"}, e.composer.PendingSkills())
}

func skillEditor(t *testing.T) *Editor {
	t.Helper()
	dir := t.TempDir()
	// Write one skill under <dir>/skills/<name>/SKILL.md so the registry root
	// is the temp dir's skills folder.
	skillDir := filepath.Join(dir, "skills", "extract-and-distill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: extract-and-distill
description: Distill ideas from source material
---
Do the work.
`), 0o644))

	bus := controller.NewBus(nil)
	e := &Editor{
		composer: composer.NewComposerPane(components.DefaultTheme(), "m", dir),
		bus:      bus,
		cwd:      dir,
	}
	e.composer.Wire(composer.WireDeps{})

	registry := commands.NewCommandRegistry()
	builtins := commands.NewBuiltinRegistry(
		registry,
		commands.NewContext(bus, nil),
		bus,
		nil,
		e.composer,
		nil,
		dir,
		nil,
		[]string{filepath.Join(dir, "skills")},
		e,
		nil,
	)
	e.commands = builtins.Registry

	return e
}

func skillOverlay(t *testing.T, e *Editor) string {
	t.Helper()
	overlay, ok := e.composer.ListOverlay(components.DrawContext{
		Max:    components.Size{Width: 100, Height: 30},
		Method: xui.WidthUnicode,
	})
	require.True(t, ok, "/skill must open the picker")
	return components.SurfaceText(overlay.Surface)
}
