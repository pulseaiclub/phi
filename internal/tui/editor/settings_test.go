package editor

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/app"
	"github.com/pulseaiclub/phi/internal/components/palette"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tui/commands"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func TestThinkingPaletteSavesPreferenceAndLoadsOnRestart(t *testing.T) {
	e, _ := newTreeEditor(t)
	bridge := newCommandBridge(e.bus, e.composer, e.ctrl, e.submitter, e.sessions, e.extCmds, nil, "")
	var menu palette.PaletteCommand
	for _, cmd := range e.commands.BuildPalette(bridge.context()) {
		if cmd.ID == "settings-thinking" {
			menu = cmd
		}
	}
	require.Len(t, menu.Submenu, 2)
	assert.False(t, e.ctrl.ThinkingExpanded())
	menu.Submenu[1].Run()
	e.drainBus()
	assert.True(t, e.ctrl.ThinkingExpanded())
	proj, err := project.Discover(e.cwd)
	require.NoError(t, err)
	bus := controller.NewBus(nil)
	ctrl, err := controller.NewController(bus, proj, e.cwd)
	require.NoError(t, err)
	t.Cleanup(ctrl.Close)
	assert.True(t, ctrl.ThinkingExpanded())
	restarted := NewEditor(app.NewApp(nil), bus, ctrl, nil, e.theme, e.cwd, "test-model", "", 10000, nil)
	restarted.transcript.ApplySession(session.AssistantMessageUpdate{Message: session.Message{
		ID: "thinking", Role: session.RoleAssistant, State: session.StateStreaming,
		Content: []session.ContentBlock{{Type: session.BlockThinking, Text: "visible reasoning body"}},
	}})
	restarted.transcript.Sync()
	before := restarted.transcript.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 20}}, 80, 20)
	assert.Contains(t, components.SurfaceText(before), "visible reasoning body")
	menu.Submenu[0].Run()
	e.drainBus()
	assert.False(t, e.ctrl.ThinkingExpanded())
	require.NoError(t, proj.LoadConfig())
	assert.False(t, proj.Config().TUI.ThinkingExpanded)
	// A failed save must leave the active preference intact.
	require.NoError(t, os.WriteFile(proj.Global().ConfigFile(), []byte("tui: ["), 0o600))
	e.Update(controller.ThinkingExpandedMsg{Expanded: true})
	assert.False(t, e.ctrl.ThinkingExpanded())
}

func TestThinkingCommandDefaultsToCollapsedOption(t *testing.T) {
	var selected []bool
	cmd := commands.ThinkingCommand(func(expanded bool) { selected = append(selected, expanded) })
	cmd.Submenu[0].Run()
	cmd.Submenu[1].Run()
	assert.Equal(t, []bool{false, true}, selected)
}
