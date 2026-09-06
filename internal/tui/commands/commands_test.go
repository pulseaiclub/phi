package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func TestThemeCommand_Submenu(t *testing.T) {
	var got string
	cmd := ThemeCommand(func(name string) { got = name })
	assert.Equal(t, "settings", cmd.Noun)
	assert.Equal(t, "theme", cmd.Verb)
	assert.Equal(t, "Select Theme", cmd.SubmenuTitle)
	require.Len(t, cmd.Submenu, 4)
	assert.Equal(t, "Dark (builtin)", cmd.Submenu[0].Verb)
	assert.Equal(t, "Pink (builtin)", cmd.Submenu[2].Verb)

	cmd.Submenu[2].Run()
	assert.Equal(t, "Pink", got)
}

func TestPermissionsCommand_Toggle(t *testing.T) {
	var bypass *bool
	cmd := PermissionsCommand(func(v bool) { bypass = &v })
	assert.Equal(t, "settings", cmd.Noun)
	assert.Equal(t, "permissions", cmd.Verb)
	require.Len(t, cmd.Submenu, 2)

	cmd.Submenu[0].Run()
	require.NotNil(t, bypass)
	assert.True(t, *bypass)

	cmd.Submenu[1].Run()
	assert.False(t, *bypass)
}

func TestAgentsCommand_Toggle(t *testing.T) {
	var enabled *bool
	cmd := AgentsCommand(func(v bool) { enabled = &v })
	assert.Equal(t, "settings", cmd.Noun)
	assert.Equal(t, "agents", cmd.Verb)
	require.Len(t, cmd.Submenu, 2)

	cmd.Submenu[0].Run()
	require.NotNil(t, enabled)
	assert.True(t, *enabled)

	cmd.Submenu[1].Run()
	assert.False(t, *enabled)
}

func TestExtensionsCommand_ListAndReload(t *testing.T) {
	var reloaded bool
	var pushedTitle string
	var pushed []palette.PaletteCommand
	cmd := ExtensionsCommand(func() []palette.PaletteCommand {
		return []palette.PaletteCommand{{
			ID:       "ext-demo",
			Verb:     "demo  [project]",
			Disabled: true,
		}}
	}, func() { reloaded = true }, func(title string, cmds []palette.PaletteCommand) {
		pushedTitle = title
		pushed = cmds
	})

	assert.Equal(t, "extensions", cmd.Noun)
	assert.Equal(t, "manage", cmd.Verb)
	require.Len(t, cmd.Submenu, 2)

	cmd.Submenu[0].Run() // list → PushSubmenu
	assert.Equal(t, "Extensions on disk", pushedTitle)
	require.NotEmpty(t, pushed)
	assert.Equal(t, "ext-demo", pushed[0].ID)

	cmd.Submenu[1].Run() // reload
	assert.True(t, reloaded)
}

func TestExtensionListEntries(t *testing.T) {
	entries := ExtensionListEntries(nil, nil, nil)
	require.Len(t, entries, 1)
	assert.True(t, entries[0].Disabled)
	assert.Contains(t, entries[0].Verb, "No extensions")

	entries = ExtensionListEntries(nil, []extension.Warning{{Path: "x", Message: "bad"}}, nil)
	require.Len(t, entries, 1)
	assert.Contains(t, entries[0].Verb, "warn:")
}

func TestSkillsCommand_SubmenuFromDisk(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "extract-and-distill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	content := `---
name: extract-and-distill
description: Distill ideas from source material
---
Do the work.
`
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644))

	var got string
	cmd := SkillsCommand(dir, func(name string) { got = name })
	assert.Equal(t, "skills", cmd.Noun)
	assert.Equal(t, "invoke", cmd.Verb)
	require.Len(t, cmd.Submenu, 1)
	assert.Equal(t, "extract-and-distill", cmd.Submenu[0].Verb)

	cmd.Submenu[0].Run()
	assert.Equal(t, "extract-and-distill", got)
}

func TestSkillsCommand_Empty(t *testing.T) {
	cmd := SkillsCommand(t.TempDir(), nil)
	require.Len(t, cmd.Submenu, 1)
	assert.True(t, cmd.Submenu[0].Disabled)
}

func TestFilterSlashCommands(t *testing.T) {
	r := NewBuiltinRegistry()
	all := r.FilterSlash("")
	require.Len(t, all, 4)

	resu := r.FilterSlash("resu")
	require.Len(t, resu, 1)
	assert.Equal(t, "resume", resu[0].Path)
	assert.Contains(t, resu[0].Description, "Resume")

	clr := r.FilterSlash("cle")
	require.Len(t, clr, 1)
	assert.Equal(t, "clear", clr[0].Path)

	none := r.FilterSlash("zzz")
	assert.Empty(t, none)

	assert.Equal(t, "/resume ", r.LookupInsert("resume"))
	assert.Equal(t, "/sessions", r.LookupInsert("sessions"))
	assert.Equal(t, "/clear", r.LookupInsert("clear"))
	assert.Equal(t, "/tree", r.LookupInsert("tree"))
}

func TestCommandRegistry_DispatchSlash(t *testing.T) {
	r := NewBuiltinRegistry()
	var sessions, cleared int
	var resumeID string
	bus := controller.NewBus(nil)

	ctx := CommandContext{
		ShowSessions:  func() { sessions++ },
		ResumeSession: func(id string) { resumeID = id },
		ClearSession:  func() { cleared++ },
		Bus:           bus,
	}

	assert.True(t, r.DispatchSlash("/sessions", ctx))
	assert.Equal(t, 1, sessions)

	assert.True(t, r.DispatchSlash("/resume abc", ctx))
	assert.Equal(t, "abc", resumeID)

	insert, ok := r.IncompleteSlash("/resume")
	assert.True(t, ok)
	assert.Equal(t, "/resume ", insert)
	assert.Empty(t, drainToast(t, bus))

	assert.True(t, r.DispatchSlash("/clear", ctx))
	assert.Equal(t, 1, cleared)

	assert.False(t, r.DispatchSlash("/unknown", ctx))
	assert.False(t, r.DispatchSlash("not-slash", ctx))
}

func TestCommandRegistry_BuildPalette(t *testing.T) {
	r := NewBuiltinRegistry()
	var model string
	var pushed bool
	cmds := r.BuildPalette(CommandContext{
		ModelNames: []string{"gpt"},
		SetModel:   func(name string) { model = name },
		PushSubmenu: func(string, []palette.PaletteCommand) {
			pushed = true
		},
		ListExtensions: func() []palette.PaletteCommand {
			return []palette.PaletteCommand{{ID: "ext-x", Verb: "x", Disabled: true}}
		},
	})
	require.GreaterOrEqual(t, len(cmds), 6)

	// settings → model → gpt
	var modelCommand palette.PaletteCommand
	for _, cmd := range cmds {
		if cmd.ID == "settings-model" {
			modelCommand = cmd
		}
	}
	require.NotEmpty(t, modelCommand.Submenu)
	modelCommand.Submenu[0].Run()
	assert.Equal(t, "gpt", model)

	// extensions → list uses PushSubmenu
	var extCmd palette.PaletteCommand
	for _, c := range cmds {
		if c.ID == "extensions" {
			extCmd = c
			break
		}
	}
	require.Equal(t, "extensions", extCmd.ID)
	extCmd.Submenu[0].Run()
	assert.True(t, pushed)
}

func TestCommandRegistry_RegisterReplace(t *testing.T) {
	r := NewCommandRegistry()
	r.Register(Command{
		Name:  "foo",
		Slash: true,
		Run:   func(CommandContext) error { return nil },
	})
	r.Register(Command{
		Name:        "foo",
		Description: "replaced",
		Slash:       true,
		Insert:      "/foo ",
		Run:         func(CommandContext) error { return nil },
	})
	assert.Equal(t, "/foo ", r.LookupInsert("foo"))
	assert.Equal(t, "replaced", r.SlashCommands()[0].Description)
}

func TestCommandRegistry_ExtCommandsDoNotReplaceBuiltins(t *testing.T) {
	r := NewBuiltinRegistry()
	assert.False(t, r.registerExt(Command{Name: "clear", Slash: true, Insert: "/hijack"}))
	assert.Equal(t, "/clear", r.LookupInsert("clear"))

	assert.True(t, r.registerExt(Command{Name: "review", Slash: true, Insert: "/review"}))
	assert.Equal(t, "/review", r.LookupInsert("review"))
	r.clearExtCommands()
	assert.Empty(t, r.LookupInsert("review"))
	assert.Equal(t, "/clear", r.LookupInsert("clear"))
}

func TestCommandRegistry_NeedsArgs(t *testing.T) {
	r := NewCommandRegistry()
	r.Register(Command{
		Name:      "plan",
		Slash:     true,
		NeedsArgs: true,
		Run:       func(CommandContext) error { return nil },
	})
	assert.Equal(t, "/plan ", r.LookupInsert("plan"))
	insert, ok := r.IncompleteSlash("/plan")
	assert.True(t, ok)
	assert.Equal(t, "/plan ", insert)
	_, ok = r.IncompleteSlash("/plan on")
	assert.False(t, ok)

	assert.True(t, r.registerExt(Command{Name: "review", Slash: true, NeedsArgs: true}))
	assert.Equal(t, "/review ", r.LookupInsert("review"))
}

// drainToast returns the message of the last queued ToastMsg and empties the bus.
func drainToast(t *testing.T, bus *controller.Bus) string {
	t.Helper()
	var msg string
	for _, m := range bus.Drain() {
		if tm, ok := m.(controller.ToastMsg); ok {
			msg = tm.Message
		}
	}
	return msg
}
