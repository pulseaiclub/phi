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
	"github.com/pulseaiclub/phi/internal/tui/composer"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

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
	e.composer.Wire(composer.WireHooks{})

	builtins := commands.NewBuiltinRegistry(
		bus,
		nil,
		e.composer,
		nil,
		nil,
		[]string{filepath.Join(dir, "skills")},
		nil,
		nil,
	)
	e.commands = builtins.Registry
	builtins.Bind(
		nil,
		commands.NewContext(bus, nil),
		nil,
		nil,
		nil,
		nil,
		e.showSkillList,
		dir,
		func() bool { return false },
	)

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
