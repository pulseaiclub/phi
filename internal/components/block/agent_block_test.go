package block

import (
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/status"
)

func TestAgentBlockRendersTreeAndMarkdown(t *testing.T) {
	a := &AgentBlock{
		Name:   "agent_spawn",
		Detail: "find bug",
		Status: status.ToolDone,
		Children: []ChildTool{
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

// With HideName the label stands alone: "explore: find auth", not
// "agent_spawn explore: find auth".
func TestAgentBlockHideName(t *testing.T) {
	a := &AgentBlock{
		Name:     "agent_spawn",
		Detail:   "explore: find auth",
		HideName: true,
		Status:   status.ToolDone,
		Theme:    components.DefaultTheme(),
	}
	txt := components.SurfaceText(a.Draw(components.DrawContext{Max: components.Size{Width: 80, Height: 5}}))
	require.Contains(t, txt, "explore: find auth")
	require.NotContains(t, txt, "agent_spawn")
	require.NotContains(t, a.CopyText(), "agent_spawn")
}

func TestAgentBlockSummaryCacheTransitions(t *testing.T) {
	a := &AgentBlock{
		Name: "agent_spawn", Summary: blockCacheMarkdown, Expanded: true, Status: status.ToolRunning,
		Children: []ChildTool{{Name: "read", Detail: "main.go", Status: status.ToolRunning}},
		Spinner:  status.NewSpinner(components.DefaultTheme().Foreground),
	}
	ctx := components.DrawContext{Max: components.Size{Width: 48}, Method: xui.WidthUnicode}
	a.Draw(ctx)
	steps := []struct {
		name   string
		change func()
	}{
		{"warm", func() {}},
		{"summary", func() { a.Summary += "\nNew findings 中文." }},
		{"theme", func() { a.Theme = components.DefaultTheme(); a.Theme.Foreground = a.Theme.Destructive }},
		{"default theme", func() { a.Theme = components.Theme{} }},
		{"narrow", func() { ctx.Max.Width = 13 }},
		{"method", func() { ctx.Method = xui.WidthMethod(1) }},
		{"fallback width", func() { ctx.Max.Width = 0 }},
		{"animation", func() { a.Spinner.Tick() }},
		{"status", func() { a.Status = status.ToolDone; a.Children[0].Status = status.ToolDone }},
		{"error", func() { a.Error = "failed" }},
		{"title", func() { a.Detail = "updated title"; a.HideName = true }},
		{"collapse", func() { a.Expanded = false }},
		{"expand", func() { a.Expanded = true }},
		{"empty", func() { a.Summary = " \n " }},
		{"restore", func() { a.Summary = blockCacheMarkdown }},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			step.change()
			fresh := &AgentBlock{
				Name: a.Name, Detail: a.Detail, HideName: a.HideName, Status: a.Status,
				Children: a.Children, Summary: a.Summary, Error: a.Error, Expanded: a.Expanded,
				Theme: a.Theme, Spinner: a.Spinner,
			}
			assertBlockCacheSurface(t, a, ctx, fresh.Draw(ctx))
			assert.Equal(t, fresh.titleH, a.titleH)
		})
	}
}
