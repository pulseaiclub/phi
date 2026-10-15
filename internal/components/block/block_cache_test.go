package block

import (
	"strings"
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/status"
	"github.com/pulseaiclub/phi/internal/session"
)

const blockCacheMarkdown = "## Rendering 缓存\n\nA **bold** response with `inline code`, 中文换行 and 👩‍💻.\n\n" +
	"```go\npackage main\n\nfunc main() {\n\tprintln(\"你好，世界\")\n}\n```\n"

func assertBlockCacheSurface(
	t *testing.T,
	widget components.Widget,
	ctx components.DrawContext,
	want components.Surface,
) {
	t.Helper()
	got := widget.Draw(ctx)
	assert.Equal(t, want.Size, got.Size)
	assert.Equal(t, want.Buffer, got.Buffer)
	assert.Same(t, widget, got.Widget)
	require.NotEmpty(t, got.Buffer)
	for i := range got.Buffer {
		got.Buffer[i].Char = "!"
		got.Buffer[i].Style = components.DefaultTheme().Destructive
	}
	again := widget.Draw(ctx)
	assert.Equal(t, want.Buffer, again.Buffer, "surface mutations must not alter cached rendering")
	assert.NotSame(t, &got.Buffer[0], &again.Buffer[0])
}

func TestAssistantBlockCacheTransitions(t *testing.T) {
	a := &AssistantBlock{Text: blockCacheMarkdown, State: session.StateComplete}
	ctx := components.DrawContext{Max: components.Size{Width: 48}, Method: xui.WidthUnicode}
	a.Draw(ctx)
	steps := []struct {
		name   string
		change func()
	}{
		{"warm", func() {}},
		{"stream text", func() { a.Text += "\nMore 中文 text." }},
		{"error", func() { a.State = session.StateError }},
		{"cancelled", func() { a.State = session.StateCancelled }},
		{"complete", func() { a.State = session.StateComplete }},
		{"theme", func() { a.Theme = components.DefaultTheme(); a.Theme.Foreground = a.Theme.Destructive }},
		{"default theme", func() { a.Theme = components.Theme{} }},
		{"narrow", func() { ctx.Max.Width = 13 }},
		{"method", func() { ctx.Method = xui.WidthMethod(1) }},
		{"fallback width", func() { ctx.Max.Width = 0 }},
		{"empty error", func() { a.Text = ""; a.State = session.StateError }},
		{"empty cancelled", func() { a.State = session.StateCancelled }},
		{"restore", func() { a.Text = blockCacheMarkdown }},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			step.change()
			fresh := &AssistantBlock{Text: a.Text, State: a.State, Theme: a.Theme}
			assertBlockCacheSurface(t, a, ctx, fresh.Draw(ctx))
		})
	}
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

func BenchmarkAssistantBlockCache(b *testing.B) {
	for _, mode := range []string{"cold", "warm", "resize", "streaming"} {
		b.Run(mode, func(b *testing.B) {
			a := &AssistantBlock{Text: strings.Repeat(blockCacheMarkdown, 4), State: session.StateComplete}
			ctx := components.DrawContext{Max: components.Size{Width: 80}, Method: xui.WidthUnicode}
			base := a.Text
			a.Draw(ctx)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				switch mode {
				case "cold":
					a.cache.Clear()
				case "resize":
					ctx.Max.Width = 60 + i%2*20
				case "streaming":
					a.Text = base + strings.Repeat("新", i%64+1)
				}
				a.Draw(ctx)
			}
		})
	}
}
