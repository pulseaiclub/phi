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

func TestNewBuiltinRegistry_RegistersDomains(t *testing.T) {
	bus := controller.NewBus(nil)
	comp := &stubBuiltinComposer{}
	var opened []string

	b := NewBuiltinRegistry(
		bus,
		nil,
		comp,
		nil,
		nil,
		[]string{"m1"},
		t.TempDir(),
		func(args []string) { opened = append([]string(nil), args...) },
		nil,
	)
	require.NotNil(t, b)
	require.NotNil(t, b.Registry)
	require.NotNil(t, b.Sessions)
	require.NotNil(t, b.Ext)

	assert.Equal(t, "/sessions", b.Registry.LookupInsert("sessions"))
	assert.Equal(t, "/new", b.Registry.LookupInsert("new"))
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
	assert.True(t, ids["skills"])

	assert.Equal(t, "/branch ", b.Registry.LookupInsert("branch"))

	assert.True(t, b.Registry.DispatchSlash("/diff staged", ctx))
	assert.Equal(t, []string{"staged"}, opened)

	b.Bind(nil, nil, nil, nil, nil, func() bool { return true })
	b.Sessions.NewSession()
	assert.Contains(t, drainToast(t, bus), "Cannot start a new session")
}
