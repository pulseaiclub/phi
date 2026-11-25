package block

import (
	"strings"
	"testing"

	"github.com/pulseaiclub/xui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/session"
)

// cellCheck spot-checks the style painted at one buffer cell.
type cellCheck struct {
	x, y  int
	style xui.Style
}

func TestAssistantBlockDraw(t *testing.T) {
	th := components.DefaultTheme()

	tests := []struct {
		name           string
		text           string
		state          session.State
		width          int // Max.Width; <= 0 exercises the 40 default
		wantWidth      int
		wantMinHeight  int
		wantContains   []string
		wantNotContain []string
		wantCells      []cellCheck
	}{
		{
			name:         "plain text",
			text:         "hello world",
			width:        60,
			wantWidth:    60,
			wantContains: []string{"hello world"},
		},
		{
			name:         "empty width falls back to 40",
			text:         "hi",
			width:        0,
			wantWidth:    40,
			wantContains: []string{"hi"},
		},
		{
			name:          "hard newlines produce rows",
			text:          "line one\nline two",
			width:         60,
			wantWidth:     60,
			wantMinHeight: 2,
			wantContains:  []string{"line one", "line two"},
		},
		{
			name:          "long text soft-wraps",
			text:          strings.Repeat("word ", 20),
			width:         12,
			wantWidth:     12,
			wantMinHeight: 3,
			wantContains:  []string{"word word"},
		},
		{
			name:           "inline code strips backticks and highlights",
			text:           "run `go test` now",
			width:          60,
			wantWidth:      60,
			wantContains:   []string{"run ", "go test", " now"},
			wantNotContain: []string{"`"},
			wantCells: []cellCheck{
				{x: 0, y: 0, style: th.Foreground}, // "run "
				{x: 4, y: 0, style: th.ToolName},   // code token
			},
		},
		{
			name:         "paths highlighted",
			text:         "see internal/components/block.go",
			width:        60,
			wantWidth:    60,
			wantContains: []string{"internal/components/block.go"},
			wantCells: []cellCheck{
				{x: 4, y: 0, style: th.ToolName}, // path token start
			},
		},
		{
			name:         "error renders red prefixed body",
			text:         "boom",
			state:        session.StateError,
			width:        60,
			wantWidth:    60,
			wantContains: []string{"Error: boom"},
			wantCells: []cellCheck{
				{x: 0, y: 0, style: th.Destructive}, // "Error:" label
				{x: 7, y: 0, style: th.Destructive}, // error body
			},
		},
		{
			name:           "error with empty text renders no label",
			state:          session.StateError,
			width:          60,
			wantWidth:      60,
			wantNotContain: []string{"Error:"},
		},
		{
			name:          "cancelled appends muted label",
			text:          "partial",
			state:         session.StateCancelled,
			width:         60,
			wantWidth:     60,
			wantMinHeight: 2,
			wantContains:  []string{"partial", "(cancelled)"},
			wantCells: []cellCheck{
				{x: 0, y: 1, style: th.Muted}, // "(cancelled)" row
			},
		},
		{
			name:           "cancelled with empty text renders no label",
			state:          session.StateCancelled,
			width:          60,
			wantWidth:      60,
			wantNotContain: []string{"cancelled"},
		},
		{
			name:           "complete state renders no label",
			text:           "done",
			state:          session.StateComplete,
			width:          60,
			wantWidth:      60,
			wantContains:   []string{"done"},
			wantNotContain: []string{"cancelled"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assistantBlock := &AssistantBlock{
				Text:  tt.text,
				State: tt.state,
				Theme: th,
			}
			surface := assistantBlock.Draw(components.DrawContext{
				Max:    components.Size{Width: tt.width},
				Method: xui.WidthUnicode,
			})

			// The surface must carry the block as its widget identity.
			assert.Same(t, assistantBlock, surface.Widget)

			assert.Equal(t, tt.wantWidth, surface.Size.Width, "surface width")
			assert.GreaterOrEqual(t, surface.Size.Height, max(1, tt.wantMinHeight), "surface height")

			txt := components.SurfaceText(surface)
			for _, want := range tt.wantContains {
				assert.Contains(t, txt, want)
			}
			for _, notWant := range tt.wantNotContain {
				assert.NotContains(t, txt, notWant)
			}

			for _, cc := range tt.wantCells {
				require.True(t, cc.y < surface.Size.Height && cc.x < surface.Size.Width,
					"cell (%d,%d) outside surface %dx%d", cc.x, cc.y, surface.Size.Width, surface.Size.Height)
				got := surface.Buffer[cc.y*surface.Size.Width+cc.x]
				assert.True(
					t,
					cc.style.Equal(got.Style),
					"style at (%d,%d): want %+v, got %+v (char %q)",
					cc.x,
					cc.y,
					cc.style,
					got.Style,
					got.Char,
				)
			}
		})
	}
}

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

const cjkHowTo = `【怎么做】
1. 翻出几篇你满意的笔记，找出共同点：标题怎么起、开头几句话说什么、分几段、写多少字
2. 把规律一条条写清楚。越具体越好：别写"写得好一点"，要写"每段1-2行，全篇不超过800字"
3. 把每次会变的部分用{{占位符}}代替，比如{{主题}}
4. 让AI照着写一篇试试，哪里不对就改说明书，改到满意为止`

func TestCJKSampleNoBlankGapsAfterClearRender(t *testing.T) {
	th := components.DefaultTheme()
	a := &AssistantBlock{Text: cjkHowTo, Theme: th}
	const w = 80
	s := a.Draw(components.DrawContext{Max: components.Size{Width: w, Height: 20}, Method: xui.WidthUnicode})

	screen := xui.NewScreen(w, s.Size.Height+2)
	win := xui.NewWindow(screen)
	win.Clear()
	s.Render(win)

	txt := components.SurfaceText(s)
	for _, want := range []string{"翻出几篇你满意的", "把每次", "笔记"} {
		require.Contains(t, txt, want, "surface missing %q", want)
	}

	var b strings.Builder
	_, h := screen.Size()
	for y := range h {
		for x := 0; x < w; {
			c := screen.GetCell(x, y)
			step := int(c.Width)
			step = max(step, 1)
			if !c.Trail && c.Char != "" && c.Char != " " {
				b.WriteString(c.Char)
			}
			x += step
		}
		b.WriteByte('\n')
	}
	got := b.String()
	for _, want := range []string{"翻出几篇你满意的", "把每次"} {
		require.Contains(t, got, want, "screen missing %q\n%s", want, got)
	}

	screen.MarkRefresh()
	for _, d := range screen.Diff() {
		require.False(t, d.Cell.Trail, "Diff emitted Trail at (%d,%d)", d.X, d.Y)
	}
}

func TestUserAndAssistant(t *testing.T) {
	u := &UserBlock{Text: "hello", Theme: components.DefaultTheme()}
	us := u.Draw(components.DrawContext{Max: components.Size{Width: 40, Height: 5}})
	usText := components.SurfaceText(us)
	require.True(t, strings.Contains(usText, "$ hello") || strings.Contains(usText, "hello"),
		"user: %q", usText)
	a := &AssistantBlock{Text: "see `xui` and examples/", Theme: components.DefaultTheme()}
	as := a.Draw(components.DrawContext{Max: components.Size{Width: 60, Height: 5}})
	txt := components.SurfaceText(as)
	require.Contains(t, txt, "xui")
	require.Contains(t, txt, "examples")
}
