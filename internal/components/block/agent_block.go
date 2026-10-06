package block

import (
	"strings"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/status"
	"github.com/pulseaiclub/phi/internal/components/text"
	"github.com/pulseaiclub/phi/internal/components/tree"
)

// ChildTool is one nested tool row under an AgentBlock (sub-agent tree).
type ChildTool struct {
	Name   string
	Detail string
	Status status.ToolStatus
}

// AgentBlock renders agent_spawn / agent_wait with an optional
// nested tool tree and a terminal markdown summary (not raw JSON).
type AgentBlock struct {
	Name     string
	Detail   string
	Status   status.ToolStatus
	Children []ChildTool
	Summary  string // markdown; shown only when set
	Error    string
	Expanded bool
	Theme    components.Theme
	Spinner  *status.Spinner
	OnToggle func(expanded bool)

	titleH int
}

func (a *AgentBlock) theme() components.Theme {
	if a.Theme.Success.Fg.Kind == 0 && a.Theme.Foreground.Fg.Kind == 0 {
		return components.DefaultTheme()
	}
	return a.Theme
}

func (a *AgentBlock) hasBody() bool {
	return len(a.Children) > 0 || strings.TrimSpace(a.Summary) != "" || strings.TrimSpace(a.Error) != ""
}

// Handle toggles expansion on Enter/space. Mouse clicks arrive through ClickAt.
func (a *AgentBlock) Handle(ctx *components.EventContext, ev xui.Event) {
	if !a.hasBody() || !toggleKey(ev) {
		return
	}
	a.toggle()
	ctx.ConsumeAndRedraw()
}

// ClickAt toggles when the click lands on the title row.
func (a *AgentBlock) ClickAt(_, y int) bool {
	if !a.hasBody() || !titleHit(a.titleH, y) {
		return false
	}
	a.toggle()
	return true
}

// toggle flips expansion and reports the new state to OnToggle.
func (a *AgentBlock) toggle() {
	a.Expanded = !a.Expanded
	if a.OnToggle != nil {
		a.OnToggle(a.Expanded)
	}
}

// CopyText returns name, detail, child lines, and summary.
func (a *AgentBlock) CopyText() string {
	var b strings.Builder
	b.WriteString(a.Name)
	if a.Detail != "" {
		b.WriteByte(' ')
		b.WriteString(a.Detail)
	}
	st := tree.DefaultStyle()
	for i, c := range a.Children {
		b.WriteByte('\n')
		b.WriteString(tree.PrefixForSiblings(len(a.Children), i, st))
		b.WriteString(chrome.ChildIcon(c.Status))
		b.WriteByte(' ')
		b.WriteString(c.Name)
		if c.Detail != "" {
			b.WriteByte(' ')
			b.WriteString(c.Detail)
		}
	}
	if sum := strings.TrimSpace(a.Summary); sum != "" {
		b.WriteByte('\n')
		b.WriteString(sum)
	}
	if err := strings.TrimSpace(a.Error); err != "" {
		b.WriteByte('\n')
		b.WriteString("Error: ")
		b.WriteString(err)
	}
	return b.String()
}

// Draw renders the agent title (icon + name + detail), the nested tool tree,
// and the markdown summary / error body when expanded.
func (a *AgentBlock) Draw(ctx components.DrawContext) components.Surface {
	th := a.theme()
	w := ctx.Max.Width
	if w <= 0 {
		w = 40
	}

	icon, iconSt := chrome.ToolIcon(a.Status, th, a.Spinner)
	spans := []components.Span{
		{Text: icon + " ", Style: iconSt},
		{Text: a.Name, Style: th.ToolName},
	}
	if a.Detail != "" {
		spans = append(spans, components.Span{Text: " " + a.Detail, Style: th.Muted})
	}
	if suf := chrome.StatusSuffix(a.Status); suf != "" {
		spans = append(spans, components.Span{Text: suf, Style: th.Muted})
	}
	if a.hasBody() {
		spans = append(spans, components.Span{Text: chrome.ExpandArrow(a.Expanded), Style: th.Muted})
	}

	titleLines := components.WrapSpans(spans, w, ctx.Method)
	a.titleH = len(titleLines)

	var treeLines []components.RichLine
	var footLines []components.RichLine
	if a.Expanded && a.hasBody() {
		bodyW := w
		if bodyW > 2 {
			bodyW -= 2
		}
		st := tree.DefaultStyle()
		n := len(a.Children)
		for i, c := range a.Children {
			prefix := tree.PrefixForSiblings(n, i, st)
			cIcon, cSt := chrome.ToolIcon(c.Status, th, a.Spinner)
			row := []components.Span{
				{Text: prefix, Style: th.Muted},
				{Text: cIcon + " ", Style: cSt},
				{Text: c.Name, Style: th.ToolName},
			}
			if c.Detail != "" {
				row = append(row, components.Span{Text: " " + c.Detail, Style: th.Muted})
			}
			treeLines = append(treeLines, components.WrapSpans(row, w, ctx.Method)...)
		}
		if err := strings.TrimSpace(a.Error); err != "" {
			footLines = append(footLines, components.WrapSpans([]components.Span{
				{Text: "Error: " + err, Style: th.Destructive},
			}, bodyW, ctx.Method)...)
		}
		if sum := strings.TrimSpace(a.Summary); sum != "" {
			md := text.RenderMarkdown(sum, th)
			footLines = append(footLines, components.WrapSpans(md, bodyW, ctx.Method)...)
		}
	}

	h := len(titleLines) + len(treeLines) + len(footLines)
	h = max(h, 1)
	s := components.NewSurface(w, h, a)
	y := 0
	for _, line := range titleLines {
		components.PaintSpans(&s, 0, y, line, ctx.Method)
		y++
	}
	for _, line := range treeLines {
		components.PaintSpans(&s, 0, y, line, ctx.Method)
		y++
	}
	for _, line := range footLines {
		components.PaintSpans(&s, 2, y, line, ctx.Method)
		y++
	}
	return s
}
