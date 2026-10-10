package editor

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/tui/commands"
	"github.com/pulseaiclub/phi/internal/tui/composer"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// TestEditorAppliesBranchLabel removed: it constructed an Editor with a nil vx
// and only passed because Update nil-guarded vx before QueueRefresh. Guard is
// gone — a nil vx now panics, which is the intended contract.
// TestBranchSlashOpensPickerOverRealRepo walks the whole path the way the shell
// wires it: registry → BranchCommands → git → branchlist → composer overlay.
func TestBranchSlashOpensPickerOverRealRepo(t *testing.T) {
	dir := gitFixtureRepo(t)
	bus := controller.NewBus(nil)
	e := &Editor{
		composer: composer.NewComposerPane(components.DefaultTheme(), "m", dir),
		bus:      bus,
		cwd:      dir,
	}
	e.composer.Wire(nil, nil, nil, "", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	builtins := commands.NewBuiltinRegistry(bus, nil, e.composer, nil, nil, nil, "", nil, nil)
	e.commands = builtins.Registry
	builtins.Bind(
		nil,
		func() commands.Context { return commands.NewContext(bus, nil) },
		nil,
		nil,
		e.composer.ShowBranchList,
		func() string { return dir },
		func() bool { return false },
	)

	require.True(t, e.commands.DispatchSlash("/branch", commands.NewContext(bus, nil)))

	overlay, ok := e.composer.ListOverlay(components.DrawContext{
		Max:    components.Size{Width: 100, Height: 30},
		Method: xui.WidthUnicode,
	})
	require.True(t, ok, "/branch must open the picker")
	text := components.SurfaceText(overlay.Surface)
	assert.Contains(t, text, "Branches")
	assert.Contains(t, text, "main")
	assert.NotContains(t, text, "current", "two columns only — no badge column")
}

func gitFixtureRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	git := func(args ...string) {
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
	git("init", "-b", "main")
	git("config", "user.email", "phi@example.com")
	git("config", "user.name", "phi tests")
	git("commit", "--allow-empty", "-m", "init")
	return dir
}
