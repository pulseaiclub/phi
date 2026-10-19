package editor

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/tui/commands"
	"github.com/pulseaiclub/phi/internal/tui/composer"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/util/gitx"
)

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

	out, err := exec.CommandContext(t.Context(), "git", "branch", "--list", "old-work").Output()
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(string(out)), "git must have dropped the branch")
	assert.NotContains(t, branchOverlay(t, e), "old-work", "the open picker refreshed its rows")
	assert.Contains(t, branchOverlay(t, e), "main")
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
	e.composer.Wire(nil, nil, nil, "", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	builtins := commands.NewBuiltinRegistry(bus, nil, e.composer, nil, nil, nil, nil, nil, nil)
	e.commands = builtins.Registry
	builtins.Bind(
		nil,
		func() commands.Context { return commands.NewContext(bus, nil) },
		nil,
		nil,
		func(branches []gitx.Branch, recent []string, onAccept, onDelete func(name string)) {
			e.composer.ShowBranchList(branches, recent, onAccept, onDelete)
		},
		e.composer.RefreshBranchList,
		func() string { return dir },
		func() bool { return false },
	)
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
