package block

import (
	"strings"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/status"
)

// ToolBlock renders a generic tool_use row: status glyph, name,
// detail, and optional expandable output body.
type ToolBlock struct {
	Name     string
	Detail   string
	Output   string
	Error    string
	Status   status.ToolStatus
	Expanded bool
	Theme    components.Theme
	Spinner  *status.Spinner
	OnToggle func(expanded bool)

	titleH int
}

func (toolBlock *ToolBlock) theme() components.Theme {
	if toolBlock.Theme.Success.Fg.Kind == 0 && toolBlock.Theme.Foreground.Fg.Kind == 0 {
		return components.DefaultTheme()
	}
	return toolBlock.Theme
}

func (toolBlock *ToolBlock) hasBody() bool {
	return strings.TrimSpace(toolBlock.Output) != "" || strings.TrimSpace(toolBlock.Error) != ""
}

// Handle toggles expansion on Enter/space. Mouse clicks arrive through ClickAt.
func (toolBlock *ToolBlock) Handle(ctx *components.EventContext, ev xui.Event) {
	if !toolBlock.hasBody() || !toggleKey(ev) {
		return
	}
	toolBlock.toggle()
	ctx.ConsumeAndRedraw()
}

// ClickAt toggles when the click lands on the title row.
func (toolBlock *ToolBlock) ClickAt(_, y int) bool {
	if !toolBlock.hasBody() || !titleHit(toolBlock.titleH, y) {
		return false
	}
	toolBlock.toggle()
	return true
}

// toggle flips expansion and reports the new state to OnToggle.
func (toolBlock *ToolBlock) toggle() {
	toolBlock.Expanded = !toolBlock.Expanded
	if toolBlock.OnToggle != nil {
		toolBlock.OnToggle(toolBlock.Expanded)
	}
}

// CopyText returns name, detail, and body.
func (toolBlock *ToolBlock) CopyText() string {
	var b strings.Builder
	b.WriteString(toolBlock.Name)
	if toolBlock.Detail != "" {
		b.WriteByte(' ')
		b.WriteString(toolBlock.Detail)
	}
	if out := strings.TrimSpace(toolBlock.Output); out != "" {
		b.WriteByte('\n')
		b.WriteString(out)
	}
	if err := strings.TrimSpace(toolBlock.Error); err != "" {
		b.WriteByte('\n')
		b.WriteString("Error: ")
		b.WriteString(err)
	}
	return b.String()
}

// Draw renders the tool status glyph, name, detail, and optional output body.
func (toolBlock *ToolBlock) Draw(ctx components.DrawContext) components.Surface {
	th := toolBlock.theme()
	w := ctx.Max.Width
	if w <= 0 {
		w = 40
	}

	icon, iconSt := chrome.ToolIcon(toolBlock.Status, th, toolBlock.Spinner)
	spans := []components.Span{
		{Text: icon + " ", Style: iconSt},
		{Text: toolBlock.Name, Style: th.ToolName},
	}
	if toolBlock.Detail != "" {
		spans = append(spans, components.Span{Text: " " + toolBlock.Detail, Style: th.Muted})
	}
	if suf := chrome.StatusSuffix(toolBlock.Status); suf != "" {
		spans = append(spans, components.Span{Text: suf, Style: th.Muted})
	}
	if toolBlock.hasBody() {
		spans = append(spans, components.Span{Text: chrome.ExpandArrow(toolBlock.Expanded), Style: th.Muted})
	}

	titleLines := components.WrapSpans(spans, w, ctx.Method)
	toolBlock.titleH = len(titleLines)

	var bodyLines []components.RichLine
	if toolBlock.Expanded && toolBlock.hasBody() {
		bodyW := w
		if bodyW > 2 {
			bodyW -= 2
		}
		if err := strings.TrimSpace(toolBlock.Error); err != "" {
			bodyLines = append(bodyLines, components.WrapSpans([]components.Span{
				{Text: "Error: " + err, Style: th.Destructive},
			}, bodyW, ctx.Method)...)
		}
		if out := strings.TrimSpace(toolBlock.Output); out != "" {
			fg := th.Foreground
			fg.Dim = true
			bodyLines = append(bodyLines, components.WrapSpans([]components.Span{
				{Text: out, Style: fg},
			}, bodyW, ctx.Method)...)
		}
	}

	h := len(titleLines) + len(bodyLines)
	h = max(h, 1)
	s := components.NewSurface(w, h, toolBlock)
	y := 0
	for _, line := range titleLines {
		components.PaintSpans(&s, 0, y, line, ctx.Method)
		y++
	}
	for _, line := range bodyLines {
		components.PaintSpans(&s, 2, y, line, ctx.Method)
		y++
	}
	return s
}
