package block

import (
	"strings"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/rendercache"
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
	Name   string
	Detail string
	// HideName drops the tool name from the title: the label is already
	// self-describing ("explore: find auth"), so "agent_spawn" is noise.
	HideName bool
	Status   status.ToolStatus
	Children []ChildTool
	Summary  string // markdown; shown only when set
	Error    string
	Expanded bool
	Theme    components.Theme
	Spinner  *status.Spinner
	OnToggle func(expanded bool)

	titleH       int
	summaryCache rendercache.Cache[agentSummaryRenderKey]
}

type agentSummaryRenderKey struct {
	text  string
	theme components.Theme
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

// CopyText returns the title, child lines, and summary.
func (a *AgentBlock) CopyText() string {
	var b strings.Builder
	var head []string
	if !a.HideName {
		head = append(head, a.Name)
	}
	if a.Detail != "" {
		head = append(head, a.Detail)
	}
	b.WriteString(strings.Join(head, " "))
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
	spans := []components.Span{{Text: icon + " ", Style: iconSt}}
	if !a.HideName {
		spans = append(spans, components.Span{Text: a.Name, Style: th.ToolName})
	}
	if a.Detail != "" {
		text := a.Detail
		if !a.HideName {
			text = " " + text
		}
		spans = append(spans, components.Span{Text: text, Style: th.Muted})
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
			guide := ""
			if i < n-1 {
				guide = st.Vertical
			}
			treeLines = append(treeLines, a.childToolLines(c, prefix, guide, th, w, ctx.Method)...)
		}
		if err := strings.TrimSpace(a.Error); err != "" {
			footLines = append(footLines, components.WrapSpans([]components.Span{
				{Text: "Error: " + err, Style: th.Destructive},
			}, bodyW, ctx.Method)...)
		}
		if sum := strings.TrimSpace(a.Summary); sum != "" {
			key := agentSummaryRenderKey{text: sum, theme: th}
			lines := a.summaryCache.Lines(key, bodyW, ctx.Method, func() []components.Span {
				return text.RenderMarkdown(sum, th)
			})
			footLines = append(footLines, lines...)
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

func (a *AgentBlock) childToolLines(
	child ChildTool,
	prefix, guide string,
	th components.Theme,
	width int,
	method xui.WidthMethod,
) []components.RichLine {
	icon, iconStyle := chrome.ToolIcon(child.Status, th, a.Spinner)
	row := []components.Span{
		{Text: prefix, Style: th.Muted},
		{Text: icon + " ", Style: iconStyle},
		{Text: child.Name, Style: th.ToolName},
	}
	if child.Detail == "" {
		return components.WrapSpans(row, width, method)
	}
	// Bash details are executable commands, so keep them readable and align
	// wrapped continuations with the command instead of the tree prefix.
	if strings.EqualFold(child.Name, bashToolName) {
		row = append(row, components.Span{Text: " ", Style: th.ToolName})
		return wrapCommand(row, []components.Span{
			{Text: child.Detail, Style: th.Foreground},
		}, guide, width, method)
	}
	row = append(row, components.Span{Text: " " + child.Detail, Style: th.Muted})
	return components.WrapSpans(row, width, method)
}
