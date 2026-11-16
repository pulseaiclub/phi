package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

type stubBuiltinComposer struct {
	model      string
	thinkLevel string
	skills     []string
	pal        []palette.PaletteCommand
}

func (s *stubBuiltinComposer) SetModelLabel(name, thinkLevel string) {
	s.model = name
	s.thinkLevel = thinkLevel
}

func (s *stubBuiltinComposer) AddPendingSkill(name string) {
	s.skills = append(s.skills, name)
}

func (s *stubBuiltinComposer) SetPaletteCommands(cmds []palette.PaletteCommand) {
	s.pal = cmds
}

func TestNewBuiltinRegistry_InjectedRegistry(t *testing.T) {
	registry := NewCommandRegistry()
	ctx := NewContext(controller.NewBus(nil), nil)
	comp := &stubBuiltinComposer{}
	registry.Register(Command{
		Name: "custom",
		Build: func(got Context) palette.PaletteCommand {
			assert.Same(t, ctx, got)
			return palette.PaletteCommand{ID: "custom"}
		},
	})

	b := NewBuiltinRegistry(
		WithBuiltinCommandRegistry(registry),
		WithBuiltinComposer(comp),
		WithBuiltinCommandContext(ctx),
	)

	assert.Same(t, registry, b.Registry)
	assert.Equal(t, "/sessions", registry.LookupInsert("sessions"))
	b.Ext.RefreshPalette()
	assert.Contains(t, comp.pal, palette.PaletteCommand{ID: "custom"})
}

func TestNewBuiltinRegistry_DefaultRegistry(t *testing.T) {
	b := NewBuiltinRegistry()
	require.NotNil(t, b.Registry)
	assert.Equal(t, "/sessions", b.Registry.LookupInsert("sessions"))
}

func TestNewBuiltinRegistry_RegistersDomains(t *testing.T) {
	bus := controller.NewBus(nil)
	comp := &stubBuiltinComposer{}
	var opened []string

	b := NewBuiltinRegistry(
		WithBuiltinBus(bus),
		WithBuiltinComposer(comp),
		WithBuiltinModelNames([]string{"m1"}),
		WithBuiltinSkillPaths([]string{t.TempDir()}),
		WithBuiltinOpenDiff(func(args []string) { opened = append([]string(nil), args...) }),
		WithBuiltinStreamActive(func() bool { return true }),
	)
	require.NotNil(t, b)
	require.NotNil(t, b.Registry)
	require.NotNil(t, b.Sessions)
	require.NotNil(t, b.Ext)

	assert.Equal(t, "/sessions", b.Registry.LookupInsert("sessions"))
	assert.Equal(t, "/new", b.Registry.LookupInsert("new"))
	assert.Equal(t, "/fork", b.Registry.LookupInsert("fork"))
	assert.Equal(t, "/diff ", b.Registry.LookupInsert("diff"))

	ctx := NewContext(bus, nil)
	paletteCmds := b.Registry.BuildPalette(ctx)
	ids := make(map[string]bool, len(paletteCmds))
	for _, c := range paletteCmds {
		ids[c.ID] = true
	}
	assert.True(t, ids["settings-model"])
	assert.True(t, ids["settings-theme"])
	assert.True(t, ids["settings-permissions"])
	assert.True(t, ids["settings-agents"])
	assert.True(t, ids["extensions"])
	assert.False(t, ids["skills"], "/skill is a slash command, not a palette entry")

	assert.Equal(t, "/sessions", b.Registry.LookupInsert("sessions"))
	assert.Equal(t, "/skill", b.Registry.LookupInsert("skill"))
	assert.Equal(t, "/branch ", b.Registry.LookupInsert("branch"))

	assert.True(t, b.Registry.DispatchSlash("/diff staged", ctx))
	assert.Equal(t, []string{"staged"}, opened)

	b.Sessions.NewSession()
	assert.Contains(t, drainToast(t, bus), "Cannot start a new session")
}
