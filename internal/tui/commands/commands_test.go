package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pulseaiclub/phi/internal/debuglog"
	"github.com/pulseaiclub/phi/internal/extension/loader"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func TestThemeCommand_Submenu(t *testing.T) {
	var got string
	cmd := buildThemePalette(func(name string) { got = name }, "Dark")
	assert.Equal(t, "settings", cmd.Noun)
	assert.Equal(t, "theme", cmd.Verb)
	assert.Equal(t, "Select Theme", cmd.SubmenuTitle)
	require.Len(t, cmd.Submenu, 4)
	assert.Equal(t, chrome.CurrentMarker(true)+"Dark (builtin)", cmd.Submenu[0].Verb)
	assert.Equal(t, chrome.CurrentMarker(false)+"Pink (builtin)", cmd.Submenu[2].Verb)

	cmd.Submenu[2].Run()
	assert.Equal(t, "Pink", got)
}

func TestPermissionsCommand_Toggle(t *testing.T) {
	var bypass *bool
	cmd := buildPermissionsPalette(func(v bool) { bypass = &v }, true)
	assert.Equal(t, "settings", cmd.Noun)
	assert.Equal(t, "permissions", cmd.Verb)
	require.Len(t, cmd.Submenu, 2)
	assert.Equal(t, chrome.CurrentMarker(true)+"off — allow all (no prompts)", cmd.Submenu[0].Verb)
	assert.Equal(t, chrome.CurrentMarker(false)+"on — ask before gated tools", cmd.Submenu[1].Verb)

	cmd.Submenu[0].Run()
	require.NotNil(t, bypass)
	assert.True(t, *bypass)

	cmd.Submenu[1].Run()
	assert.False(t, *bypass)
}

func TestAgentsCommand_Toggle(t *testing.T) {
	var enabled *bool
	cmd := buildAgentsPalette(func(v bool) { enabled = &v }, nil, nil, false, nil)
	assert.Equal(t, "settings", cmd.Noun)
	assert.Equal(t, "agents", cmd.Verb)
	require.Len(t, cmd.Submenu, 3)
	assert.Equal(t, chrome.CurrentMarker(false)+"on — register agent_* tools", cmd.Submenu[0].Verb)
	assert.Equal(t, chrome.CurrentMarker(true)+"off — no sub-agents (fewer tools)", cmd.Submenu[1].Verb)

	cmd.Submenu[0].Run()
	require.NotNil(t, enabled)
	assert.True(t, *enabled)

	cmd.Submenu[1].Run()
	assert.False(t, *enabled)
}

func TestDebugCommand_Toggle(t *testing.T) {
	prev := debuglog.Enabled()
	t.Cleanup(func() { debuglog.SetEnabled(prev) })
	debuglog.SetEnabled(false)

	bus := controller.NewBus(nil)
	s := &SettingsCommands{Bus: bus}
	var calls []bool
	cmd := buildDebugPalette(func(on bool) {
		calls = append(calls, on)
		s.setDebug(on)
	})
	assert.Equal(t, "settings-debug", cmd.ID)
	assert.Equal(t, "settings", cmd.Noun)
	assert.Equal(t, "debug", cmd.Verb)
	assert.Equal(t, "Debug Logging", cmd.SubmenuTitle)
	require.Len(t, cmd.Submenu, 2)
	assert.True(t, strings.HasPrefix(cmd.Submenu[0].Verb, chrome.CurrentMarker(false)),
		"off is current, so the on entry stays unmarked")
	assert.True(t, strings.HasPrefix(cmd.Submenu[1].Verb, chrome.CurrentMarker(true)),
		"off entry must carry the current mark")

	cmd.Submenu[0].Run() // on
	assert.Equal(t, []bool{true}, calls)
	assert.True(t, debuglog.Enabled())
	assert.Contains(t, drainToast(t, bus), debuglog.FilePath())

	cmd = buildDebugPalette(s.setDebug)
	assert.True(t, strings.HasPrefix(cmd.Submenu[0].Verb, chrome.CurrentMarker(true)), "rebuilt menu must mark on")
	assert.True(t, strings.HasPrefix(cmd.Submenu[1].Verb, chrome.CurrentMarker(false)))

	cmd.Submenu[1].Run() // off
	assert.False(t, debuglog.Enabled())
	assert.Equal(t, "Debug logging: off", drainToast(t, bus))
}

func TestAgentsCommand_RoleModels(t *testing.T) {
	var gotRole, gotName string
	cmd := buildAgentsPalette(nil, func(role, name string) {
		gotRole, gotName = role, name
	}, []string{"cheap", "strong"}, false, nil)
	require.Len(t, cmd.Submenu, 3)
	models := cmd.Submenu[2]
	assert.Equal(t, "models", models.Verb)
	require.Len(t, models.Submenu, 3)
	assert.Equal(t, "explore", models.Submenu[0].Verb)
	assert.Equal(t, "review", models.Submenu[1].Verb)
	assert.Equal(t, "worker", models.Submenu[2].Verb)

	explore := models.Submenu[0]
	require.GreaterOrEqual(t, len(explore.Submenu), 3)
	assert.Equal(t, chrome.CurrentMarker(true)+"(inherit parent)", explore.Submenu[0].Verb)
	assert.Equal(t, chrome.CurrentMarker(false)+"cheap", explore.Submenu[1].Verb)
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
	cmd := ExtensionsPalette(
		func() []palette.PaletteCommand {
			return []palette.PaletteCommand{{
				ID:       "ext-demo",
				Verb:     "demo  [project]",
				Disabled: true,
			}}
		},
		func() { reloaded = true },
		func(title string, cmds []palette.PaletteCommand) {
			pushedTitle = title
			pushed = cmds
		},
	)

	assert.Equal(t, "extensions", cmd.Noun)
	assert.Equal(t, "manage", cmd.Verb)
	require.Len(t, cmd.Submenu, 2)

	cmd.Submenu[0].Run() // list → PushSubmenu
	assert.Equal(t, "Extensions", pushedTitle)
	require.NotEmpty(t, pushed)
	assert.Equal(t, "ext-demo", pushed[0].ID)

	cmd.Submenu[1].Run() // reload
	assert.True(t, reloaded)

	// A nil push must not panic when the list row runs.
	ExtensionsPalette(nil, nil, nil)
}

func TestExtensionListEntries(t *testing.T) {
	entries := ExtensionListEntries(nil, nil, nil)
	require.Len(t, entries, 1)
	assert.True(t, entries[0].Disabled)
	assert.Contains(t, entries[0].Verb, "No extensions")

	entries = ExtensionListEntries(nil, []loader.Warning{{Path: "x", Message: "bad"}}, nil)
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
	cmd := buildSkillsPalette([]string{dir}, func(name string) { got = name })
	assert.Equal(t, "skills", cmd.Noun)
	assert.Equal(t, "invoke", cmd.Verb)
	require.Len(t, cmd.Submenu, 1)
	assert.Equal(t, "extract-and-distill", cmd.Submenu[0].Verb)

	cmd.Submenu[0].Run()
	assert.Equal(t, "extract-and-distill", got)
}

func TestSkillsCommand_Empty(t *testing.T) {
	cmd := buildSkillsPalette([]string{t.TempDir()}, nil)
	require.Len(t, cmd.Submenu, 1)
	assert.True(t, cmd.Submenu[0].Disabled)
}

func TestFilterSlashCommands(t *testing.T) {
	r := NewCommandRegistry()
	(&SessionCommands{}).Register(r)
	(&DiffCommands{}).Register(r)

	all := r.FilterSlash("")
	require.Len(t, all, 3) // sessions, new, diff

	nw := r.FilterSlash("ne")
	require.Len(t, nw, 1)
	assert.Equal(t, "new", nw[0].Path)

	none := r.FilterSlash("zzz")
	assert.Empty(t, none)

	assert.Equal(t, "/sessions", r.LookupInsert("sessions"))
	assert.Equal(t, "/new", r.LookupInsert("new"))
	assert.Equal(t, "/diff ", r.LookupInsert("diff"))
}

func TestCommandRegistry_DispatchSlash(t *testing.T) {
	r := NewCommandRegistry()
	var sessions, started int
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
		Name:  "new",
		Slash: true,
		Run: func(Context, []string) error {
			started++
			return nil
		},
	})

	assert.True(t, r.DispatchSlash("/sessions", ctx))
	assert.Equal(t, 1, sessions)

	assert.True(t, r.DispatchSlash("/new", ctx))
	assert.Equal(t, 1, started)

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
			return buildModelPalette(func(name string) { model = name }, []string{"gpt"}, "gpt")
		},
	})
	r.Register(Command{
		Name: "extensions",
		Build: func(Context) palette.PaletteCommand {
			return palette.PaletteCommand{
				ID:           "extensions",
				Noun:         "extensions",
				Verb:         "manage",
				SubmenuTitle: "Extensions",
				Submenu: []palette.PaletteCommand{
					{ID: "ext-list", Verb: "list", Run: func() { pushed = true }},
				},
			}
		},
	})
	(&SkillsCommands{SkillPaths: []string{t.TempDir()}}).Register(r)

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
	assert.False(t, r.registerExt(Command{Name: "new", Slash: true, Insert: "/hijack"}))
	assert.Equal(t, "/new", r.LookupInsert("new"))

	assert.True(t, r.registerExt(Command{Name: "review", Slash: true, Insert: "/review"}))
	assert.Equal(t, "/review", r.LookupInsert("review"))
	r.clearExtCommands()
	assert.Empty(t, r.LookupInsert("review"))
	assert.Equal(t, "/new", r.LookupInsert("new"))
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
