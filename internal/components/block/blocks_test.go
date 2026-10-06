package block_test

import (
	"strings"
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/block"
	"github.com/pulseaiclub/phi/internal/components/status"
)

func TestBashBlockRendersOutput(t *testing.T) {
	var lines []string
	for range 20 {
		lines = append(lines, "file.go")
	}
	b := &block.BashBlock{
		Command:  "ls",
		Output:   strings.Join(lines, "\n"),
		Status:   status.ToolDone,
		Expanded: true,
		Theme:    components.DefaultTheme(),
	}
	s := b.Draw(components.DrawContext{Max: components.Size{Width: 60, Height: 40}})
	joined := components.SurfaceText(s)
	require.Contains(t, joined, "$")
	require.Contains(t, joined, "ls")
	require.NotContains(t, joined, "Show more")
	require.NotContains(t, joined, "lines truncated")
	require.Contains(t, joined, "file.go")
}

func TestUserAndAssistant(t *testing.T) {
	u := &block.UserBlock{Text: "hello", Theme: components.DefaultTheme()}
	us := u.Draw(components.DrawContext{Max: components.Size{Width: 40, Height: 5}})
	usText := components.SurfaceText(us)
	require.True(t, strings.Contains(usText, "$ hello") || strings.Contains(usText, "hello"),
		"user: %q", usText)
	a := &block.AssistantBlock{Text: "see `xui` and examples/", Theme: components.DefaultTheme()}
	as := a.Draw(components.DrawContext{Max: components.Size{Width: 60, Height: 5}})
	txt := components.SurfaceText(as)
	require.Contains(t, txt, "xui")
	require.Contains(t, txt, "examples")
}

func TestAgentBlockRendersTreeAndMarkdown(t *testing.T) {
	a := &block.AgentBlock{
		Name:   "agent_spawn",
		Detail: "find bug",
		Status: status.ToolDone,
		Children: []block.ChildTool{
			{Name: "read", Detail: "a.go", Status: status.ToolDone},
			{Name: "bash", Detail: "go test", Status: status.ToolError},
		},
		Summary:  "## Findings\n\n- fixed",
		Expanded: true,
		Theme:    components.DefaultTheme(),
	}
	s := a.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 40}})
	txt := components.SurfaceText(s)
	require.Contains(t, txt, "agent_spawn")
	require.Contains(t, txt, "find bug")
	require.Contains(t, txt, "├──")
	require.Contains(t, txt, "╰──")
	require.Contains(t, txt, "read")
	require.Contains(t, txt, "bash")
	require.Contains(t, txt, "Findings")
	require.Contains(t, txt, "fixed")
	require.NotContains(t, txt, `"job_id"`)
	require.NotContains(t, txt, `"summary"`)
}

func TestUserBlockImplementsWidget(_ *testing.T) {
	var _ components.Widget = &block.UserBlock{Text: "x", Theme: components.DefaultTheme()}
}

func TestCompactionBlockShowsTokensBefore(t *testing.T) {
	b := &block.CompactionBlock{Theme: components.DefaultTheme(), TokensBefore: 15000}
	got := components.SurfaceText(b.Draw(components.DrawContext{Max: components.Size{Width: 60}}))
	require.Contains(t, got, "Compacted from 15k tokens")
}

// An unknown count (older entries, or a compaction that never reported usage)
// keeps the plain marker instead of printing "from 0 tokens".
func TestCompactionBlockWithoutTokens(t *testing.T) {
	b := &block.CompactionBlock{Theme: components.DefaultTheme()}
	got := components.SurfaceText(b.Draw(components.DrawContext{Max: components.Size{Width: 60}}))
	require.Contains(t, got, "Compacted")
	require.NotContains(t, got, "tokens")
}

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
	return []collapsible{
		{"tool", tool, tool, func() bool { return tool.Expanded }},
		{"thinking", thinking, thinking, func() bool { return thinking.Expanded }},
		{"bash", bash, bash, func() bool { return bash.Expanded }},
		{"agent", agent, agent, func() bool { return agent.Expanded }},
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
