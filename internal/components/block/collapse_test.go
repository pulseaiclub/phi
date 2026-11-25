package block_test

import (
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/block"
	"github.com/pulseaiclub/phi/internal/components/status"
)

// collapsible is one block with a disclosure title — the row the transcript pane
// has to tell apart from the start of a drag-selection.
type collapsible struct {
	name   string
	widget components.Widget
	click  components.ClickToggler
	state  func() bool
}

func collapsibleBlocks() []collapsible {
	th := components.DefaultTheme()
	tool := &block.ToolBlock{Name: "read", Detail: "a.go", Output: "line", Status: status.ToolDone, Theme: th}
	thinking := &block.ThinkingBlock{Text: "reasoning", Theme: th}
	bash := &block.BashBlock{Command: "ls", Output: "a.go", Status: status.ToolDone, Theme: th}
	agent := &block.AgentBlock{
		Name:     "agent_spawn",
		Detail:   "job",
		Children: []block.ChildTool{{Name: "read", Detail: "a.go", Status: status.ToolDone}},
		Theme:    th,
	}
	compaction := &block.CompactionBlock{TokensBefore: 43000, Summary: "## Goal\nship it", Theme: th}
	return []collapsible{
		{"tool", tool, tool, func() bool { return tool.Expanded }},
		{"thinking", thinking, thinking, func() bool { return thinking.Expanded }},
		{"bash", bash, bash, func() bool { return bash.Expanded }},
		{"agent", agent, agent, func() bool { return agent.Expanded }},
		{"compaction", compaction, compaction, func() bool { return compaction.Expanded }},
	}
}

// A bare press must stay unconsumed: the transcript pane needs it to start a
// drag-selection, so a block that expanded here would swallow the first row of
// every copy that begins on a title row.
func TestCollapsibleBlocksDoNotToggleOnPress(t *testing.T) {
	for _, tc := range collapsibleBlocks() {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &components.EventContext{}
			tc.widget.Draw(components.DrawContext{Max: components.Size{Width: 60, Height: 20}})

			tc.widget.Handle(ctx, xui.MouseEvent{X: 2, Y: 0, Action: xui.MousePress, Button: xui.MouseLeft})
			require.False(t, ctx.Consume, "mouse press must bubble to the pane")
			require.False(t, tc.state(), "press on the title row must not expand")
		})
	}
}

func TestCollapsibleBlocksToggleOnTitleClick(t *testing.T) {
	for _, tc := range collapsibleBlocks() {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &components.EventContext{}
			tc.widget.Draw(components.DrawContext{Max: components.Size{Width: 60, Height: 20}})

			require.True(t, tc.click.ClickAt(2, 0), "click on the title row toggles")
			require.True(t, tc.state())

			tc.widget.Draw(components.DrawContext{Max: components.Size{Width: 60, Height: 20}})
			require.False(t, tc.click.ClickAt(2, 1), "body clicks must not toggle")
			require.True(t, tc.state())

			tc.widget.Handle(ctx, xui.KeyEvent{Code: xui.KeyEnter, Press: true})
			require.False(t, tc.state(), "Enter still collapses a focused block")
			require.True(t, ctx.Consume, "the key path consumes the event")
		})
	}
}
