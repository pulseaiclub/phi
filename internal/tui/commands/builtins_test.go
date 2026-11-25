package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/util/gitx"
)

// stubBuiltinComposer and stubHost are shared fixtures for the commands
// package tests (session/branch/skills/commands tests wire them into domains).
type stubBuiltinComposer struct {
	model      string
	thinkLevel string
	skills     []string
	pal        []palette.PaletteCommand

	listItems  []listpicker.Item
	listAccept func(listpicker.Item)

	sessionItems  []session.SessionMeta
	sessionAccept func(id string)

	refreshedSessions []session.SessionMeta

	branchItems  []gitx.Branch
	branchRecent []string
	branchAccept func(name string)
	branchDelete func(name string)

	refreshedBranches []gitx.Branch
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

func (s *stubBuiltinComposer) ShowList(
	items []listpicker.Item,
	_ listpicker.ShowConfig,
	onAccept func(listpicker.Item),
	_ func(listpicker.Item),
) {
	s.listItems = items
	s.listAccept = onAccept
}

func (s *stubBuiltinComposer) ShowSessionList(
	items []session.SessionMeta,
	_ string,
	onAccept func(id string),
	_ func(id string),
) {
	s.sessionItems = items
	s.sessionAccept = onAccept
}

func (s *stubBuiltinComposer) RefreshSessionList(items []session.SessionMeta) {
	s.refreshedSessions = items
}

func (s *stubBuiltinComposer) ShowBranchList(
	branches []gitx.Branch,
	recent []string,
	onAccept func(name string),
	onDelete func(name string),
) {
	s.branchItems = branches
	s.branchRecent = recent
	s.branchAccept = onAccept
	s.branchDelete = onDelete
}

func (s *stubBuiltinComposer) RefreshBranchList(branches []gitx.Branch, _ []string) {
	s.refreshedBranches = branches
}

// stubHost is the HostUI stub: one aggregate for every shell callback, with a
// mutable stream flag tests flip to exercise the guards. route records which
// viewer method ran, so a /diff→OpenCode mixup cannot hide behind one slice.
type stubHost struct {
	opened    [][]string
	route     []string
	stream    bool
	activityN int
}

func (h *stubHost) OpenDiff(args []string) {
	h.open("diff", args)
}

func (h *stubHost) OpenCode(args []string) {
	h.open("code", args)
}

func (h *stubHost) open(kind string, args []string) {
	h.route = append(h.route, kind)
	h.opened = append(h.opened, args)
}

func (h *stubHost) ResetActivity() {
	h.activityN++
}

func (h *stubHost) StreamActive() bool { return h.stream }

func TestNewBuiltinRegistry_RegistersDomains(t *testing.T) {
	bus := controller.NewBus(nil)
	comp := &stubBuiltinComposer{}
	host := &stubHost{}
	r := NewCommandRegistry()

	b := NewBuiltinRegistry(
		r,
		bus,
		nil,
		comp,
		nil,
		"",
		[]string{"m1"},
		[]string{t.TempDir()},
		host,
	)
	require.NotNil(t, b)
	require.NotNil(t, b.Registry)
	require.NotNil(t, b.Sessions)

	assert.Equal(t, "/sessions", b.Registry.LookupInsert("sessions"))
	assert.Equal(t, "/new", b.Registry.LookupInsert("new"))
	assert.Equal(t, "/fork", b.Registry.LookupInsert("fork"))
	assert.Equal(t, "/compact", b.Registry.LookupInsert("compact"))
	assert.Equal(t, "/diff ", b.Registry.LookupInsert("diff"))

	ctx := NewContext(bus, nil)
	paletteCmds := b.Registry.BuildPalette(ctx)
	ids := make(map[string]bool, len(paletteCmds))
	for _, c := range paletteCmds {
		ids[c.ID] = true
	}
	assert.True(t, ids["settings-model"])
	assert.True(t, ids["settings-theme"])
	assert.True(t, ids["settings-agents"])
	assert.False(t, ids["skills"], "/skill is a slash command, not a palette entry")

	assert.Equal(t, "/sessions", b.Registry.LookupInsert("sessions"))
	assert.Equal(t, "/skill", b.Registry.LookupInsert("skill"))
	assert.Equal(t, "/branch ", b.Registry.LookupInsert("branch"))

	assert.True(t, b.Registry.DispatchSlash("/diff staged", ctx))
	assert.Equal(t, []string{"diff"}, host.route)
	assert.Equal(t, [][]string{{"staged"}}, host.opened)

	host.stream = true
	b.Sessions.NewSession()
	assert.Contains(t, drainToast(t, bus), "Cannot start a new session")
}
