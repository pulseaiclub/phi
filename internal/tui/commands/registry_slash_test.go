package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func TestFilterSlashCommands(t *testing.T) {
	r := NewCommandRegistry()
	slash := func(name, insert string) Command {
		return Command{Name: name, Slash: true, Insert: insert, Run: func(Context, []string) {}}
	}
	r.Register(slash("sessions", "/sessions"))
	r.Register(slash("new", "/new"))
	r.Register(slash("fork", "/fork"))
	r.Register(slash("diff", "/diff "))

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
	r.Register(Command{
		Name:   "diff",
		Slash:  true,
		Insert: "/diff ",
		Run: func(_ Context, args []string) {
			spec = append([]string(nil), args...)
		},
	})
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
			return palette.PaletteCommand{
				ID: "model",
				Submenu: []palette.PaletteCommand{{
					ID:  "gpt",
					Run: func() { model = "gpt" },
				}},
			}
		},
	})
	// /skill is a slash command, not a palette entry; registering it must not
	// add anything to BuildPalette.
	r.Register(Command{
		Name:  "skill",
		Slash: true,
		Run:   func(Context, []string) {},
	})

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
