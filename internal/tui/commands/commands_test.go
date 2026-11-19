package commands

import (
	"strings"
	"testing"

	"github.com/pulseaiclub/phi/internal/debuglog"

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

func TestFilterSlashCommands(t *testing.T) {
	r := NewCommandRegistry()
	(&SessionCommands{}).Register(r)
	(&DiffCommands{}).Register(r)

	all := r.FilterSlash("")
	require.Len(t, all, 4) // sessions, new, fork, diff

	nw := r.FilterSlash("ne")
	require.Len(t, nw, 1)
	assert.Equal(t, "new", nw[0].Path)

	none := r.FilterSlash("zzz")
	assert.Empty(t, none)

	assert.Equal(t, "/sessions", r.LookupInsert("sessions"))
	assert.Equal(t, "/new", r.LookupInsert("new"))
	assert.Equal(t, "/fork", r.LookupInsert("fork"))
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
		Run: func(Context, []string) {
			sessions++
		},
	})
	r.Register(Command{
		Name:  "new",
		Slash: true,
		Run: func(Context, []string) {
			started++
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
	bus := controller.NewBus(nil)
	ctx := NewContext(bus, nil)

	r.Register(Command{
		Name: "settings-model",
		Build: func(Context) palette.PaletteCommand {
			return buildModelPalette(func(name string) { model = name }, nil, []string{"gpt"}, "gpt")
		},
	})
	// /skill is a slash command, not a palette entry; registering it must not
	// add anything to BuildPalette.
	(&SkillsCommands{Bus: bus, SkillPaths: []string{t.TempDir()}}).Register(r)

	cmds := r.BuildPalette(ctx)
	require.Len(t, cmds, 1, "only Build-registered commands reach the palette")

	require.NotEmpty(t, cmds[0].Submenu)
	cmds[0].Submenu[0].Run()
	assert.Equal(t, "gpt", model)
}

func TestCommandRegistry_RegisterReplace(t *testing.T) {
	r := NewCommandRegistry()
	r.Register(Command{
		Name:  "foo",
		Slash: true,
		Run:   func(Context, []string) {},
	})
	r.Register(Command{
		Name:        "foo",
		Description: "replaced",
		Slash:       true,
		Insert:      "/foo ",
		Run:         func(Context, []string) {},
	})
	assert.Equal(t, "/foo ", r.LookupInsert("foo"))
	assert.Equal(t, "replaced", r.SlashCommands()[0].Description)
}

func TestCommandRegistry_NeedsArgs(t *testing.T) {
	r := NewCommandRegistry()
	r.Register(Command{
		Name:      "plan",
		Slash:     true,
		NeedsArgs: true,
		Run:       func(Context, []string) {},
	})
	assert.Equal(t, "/plan ", r.LookupInsert("plan"))
	insert, ok := r.IncompleteSlash("/plan")
	assert.True(t, ok)
	assert.Equal(t, "/plan ", insert)
	_, ok = r.IncompleteSlash("/plan on")
	assert.False(t, ok)
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

func TestCodeCommandPickerAndExplicitPath(t *testing.T) {
	r := NewCommandRegistry()
	var opened [][]string
	(&CodeCommands{
		Open: func(args []string) { opened = append(opened, append([]string(nil), args...)) },
	}).Register(r)

	assert.Equal(t, "/code", r.LookupInsert("code"))

	_, incomplete := r.IncompleteSlash("/code")
	assert.False(t, incomplete, "a bare /code opens the file picker")

	ctx := NewContext(controller.NewBus(nil), nil)
	assert.True(t, r.DispatchSlash("/code", ctx))
	assert.True(t, r.DispatchSlash("/code internal/tui/editor/editor.go:42", ctx))
	require.Len(t, opened, 2)
	assert.Empty(t, opened[0])
	assert.Equal(t, []string{"internal/tui/editor/editor.go:42"}, opened[1])
}
