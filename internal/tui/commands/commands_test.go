package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/extension/manifest"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func TestThemeCommand_Submenu(t *testing.T) {
	var got string
	cmd := buildThemePalette(func(name string) { got = name })
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
	cmd := buildPermissionsPalette(func(v bool) { bypass = &v })
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
	cmd := buildAgentsPalette(func(v bool) { enabled = &v }, nil, nil)
	assert.Equal(t, "settings", cmd.Noun)
	assert.Equal(t, "agents", cmd.Verb)
	require.Len(t, cmd.Submenu, 3)

	cmd.Submenu[0].Run()
	require.NotNil(t, enabled)
	assert.True(t, *enabled)

	cmd.Submenu[1].Run()
	assert.False(t, *enabled)
}

func TestAgentsCommand_RoleModels(t *testing.T) {
	var gotRole, gotName string
	cmd := buildAgentsPalette(nil, func(role, name string) {
		gotRole, gotName = role, name
	}, []string{"cheap", "strong"})
	require.Len(t, cmd.Submenu, 3)
	models := cmd.Submenu[2]
	assert.Equal(t, "models", models.Verb)
	require.Len(t, models.Submenu, 3)
	assert.Equal(t, "explore", models.Submenu[0].Verb)
	assert.Equal(t, "review", models.Submenu[1].Verb)
	assert.Equal(t, "worker", models.Submenu[2].Verb)

	explore := models.Submenu[0]
	require.GreaterOrEqual(t, len(explore.Submenu), 3)
	assert.Equal(t, "(inherit parent)", explore.Submenu[0].Verb)
	explore.Submenu[0].Run()
	assert.Equal(t, "explore", gotRole)
	assert.Empty(t, gotName)

	explore.Submenu[2].Run() // strong
	assert.Equal(t, "explore", gotRole)
	assert.Equal(t, "strong", gotName)

	review := models.Submenu[1]
	review.Submenu[1].Run() // cheap
	assert.Equal(t, "review", gotRole)
	assert.Equal(t, "cheap", gotName)
}

func TestExtensionsCommand_ListAndReload(t *testing.T) {
	var reloaded bool
	var pushedTitle string
	var pushed []palette.PaletteCommand
	cmd := buildExtensionsPalette(func() []palette.PaletteCommand {
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

	entries = ExtensionListEntries(nil, []manifest.Warning{{Path: "x", Message: "bad"}}, nil)
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
	cmd := buildSkillsPalette(dir, func(name string) { got = name })
	assert.Equal(t, "skills", cmd.Noun)
	assert.Equal(t, "invoke", cmd.Verb)
	require.Len(t, cmd.Submenu, 1)
	assert.Equal(t, "extract-and-distill", cmd.Submenu[0].Verb)

	cmd.Submenu[0].Run()
	assert.Equal(t, "extract-and-distill", got)
}

func TestSkillsCommand_Empty(t *testing.T) {
	cmd := buildSkillsPalette(t.TempDir(), nil)
	require.Len(t, cmd.Submenu, 1)
	assert.True(t, cmd.Submenu[0].Disabled)
}

func TestFilterSlashCommands(t *testing.T) {
	r := NewCommandRegistry()
	(&SessionCommands{}).Register(r)
	(&DiffCommands{}).Register(r)

	all := r.FilterSlash("")
	require.Len(t, all, 3) // sessions, clear, diff

	clr := r.FilterSlash("cle")
	require.Len(t, clr, 1)
	assert.Equal(t, "clear", clr[0].Path)

	none := r.FilterSlash("zzz")
	assert.Empty(t, none)

	assert.Equal(t, "/sessions", r.LookupInsert("sessions"))
	assert.Equal(t, "/clear", r.LookupInsert("clear"))
	assert.Equal(t, "/diff ", r.LookupInsert("diff"))
}

func TestCommandRegistry_DispatchSlash(t *testing.T) {
	r := NewCommandRegistry()
	var sessions, cleared int
	bus := controller.NewBus(nil)
	ctx := NewContext(bus, nil)

	r.Register(Command{
		Name:  "sessions",
		Slash: true,
		Run: func(Context, []string) error {
			sessions++
			return nil
		},
	})
	r.Register(Command{
		Name:  "clear",
		Slash: true,
		Run: func(Context, []string) error {
			cleared++
			return nil
		},
	})

	assert.True(t, r.DispatchSlash("/sessions", ctx))
	assert.Equal(t, 1, sessions)

	assert.True(t, r.DispatchSlash("/clear", ctx))
	assert.Equal(t, 1, cleared)

	var spec []string
	(&DiffCommands{
		Open: func(args []string) { spec = append([]string(nil), args...) },
	}).Register(r)
	assert.True(t, r.DispatchSlash("/diff staged", ctx))
	assert.Equal(t, []string{"staged"}, spec)

	_, incomplete := r.IncompleteSlash("/diff")
	assert.False(t, incomplete, "diff args are optional; bare /diff should run")
	assert.True(t, r.DispatchSlash("/diff", ctx))
	assert.Empty(t, spec)

	assert.False(t, r.DispatchSlash("/unknown", ctx))
	assert.False(t, r.DispatchSlash("not-slash", ctx))
}

func TestCommandRegistry_BuildPalette(t *testing.T) {
	r := NewCommandRegistry()
	var model string
	var pushed bool
	bus := controller.NewBus(nil)
	ctx := NewContext(bus, func(string, []palette.PaletteCommand) {
		pushed = true
	})

	r.Register(Command{
		Name: "settings-model",
		Build: func(Context) palette.PaletteCommand {
			return buildModelPalette(func(name string) { model = name }, []string{"gpt"})
		},
	})
	r.Register(Command{
		Name: "extensions",
		Build: func(ctx Context) palette.PaletteCommand {
			var push func(string, []palette.PaletteCommand)
			if ctx != nil {
				push = ctx.PushSubmenu
			}
			return buildExtensionsPalette(func() []palette.PaletteCommand {
				return []palette.PaletteCommand{{ID: "ext-x", Verb: "x", Disabled: true}}
			}, nil, push)
		},
	})
	(&SkillsCommands{SkillPath: t.TempDir()}).Register(r)

	cmds := r.BuildPalette(ctx)
	require.GreaterOrEqual(t, len(cmds), 3)

	require.NotEmpty(t, cmds[0].Submenu)
	cmds[0].Submenu[0].Run()
	assert.Equal(t, "gpt", model)

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
		Run:   func(Context, []string) error { return nil },
	})
	r.Register(Command{
		Name:        "foo",
		Description: "replaced",
		Slash:       true,
		Insert:      "/foo ",
		Run:         func(Context, []string) error { return nil },
	})
	assert.Equal(t, "/foo ", r.LookupInsert("foo"))
	assert.Equal(t, "replaced", r.SlashCommands()[0].Description)
}

func TestCommandRegistry_ExtCommandsDoNotReplaceBuiltins(t *testing.T) {
	r := NewCommandRegistry()
	(&SessionCommands{}).Register(r)
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
		Run:       func(Context, []string) error { return nil },
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

func TestCodeCommand_NeedsArgs(t *testing.T) {
	r := NewCommandRegistry()
	var opened [][]string
	(&CodeCommands{
		Open: func(args []string) { opened = append(opened, append([]string(nil), args...)) },
	}).Register(r)

	assert.Equal(t, "/code ", r.LookupInsert("code"))

	insert, incomplete := r.IncompleteSlash("/code")
	assert.True(t, incomplete, "a bare /code waits for the user to type a path")
	assert.Equal(t, "/code ", insert)

	ctx := NewContext(controller.NewBus(nil), nil)
	assert.True(t, r.DispatchSlash("/code internal/tui/editor/editor.go:42", ctx))
	require.Len(t, opened, 1)
	assert.Equal(t, []string{"internal/tui/editor/editor.go:42"}, opened[0])
}
