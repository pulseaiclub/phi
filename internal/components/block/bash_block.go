package block

import (
	"strconv"
	"strings"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/status"
	"github.com/pulseaiclub/phi/internal/util"
)

// BashBlock renders bash tool / "!cmd" output:
//
//	$ ls
//	  parser.go
//	  ...
//	  [Showing lines 10-100 of 100. Full/retained output: /tmp/phi-bash-….log]
//
// Long output is truncated by the bash tool with a /tmp dump — this widget
// does not invent a useless "Show more" chrome.
type BashBlock struct {
	Command  string
	Output   string
	Status   status.ToolStatus
	ExitCode int
	Expanded bool
	Theme    components.Theme

	// OnToggle is called when the user expands/collapses (click title / Enter).
	OnToggle func(expanded bool)

	titleH int // title row count; only the title responds to ClickAt
}

func (bashBlock *BashBlock) theme() components.Theme {
	if bashBlock.Theme.Success.Fg.Kind == 0 && bashBlock.Theme.Foreground.Fg.Kind == 0 {
		return components.DefaultTheme()
	}
	return bashBlock.Theme
}

// Handle toggles expansion on Enter/space. Mouse clicks arrive through ClickAt.
func (bashBlock *BashBlock) Handle(ctx *components.EventContext, ev xui.Event) {
	if !bashBlock.hasBody() || !toggleKey(ev) {
		return
	}
	bashBlock.toggle()
	ctx.ConsumeAndRedraw()
}

// ClickAt toggles when the click lands on the title row; the output body stays
// selectable for copy-on-select.
func (bashBlock *BashBlock) ClickAt(_, y int) bool {
	if !bashBlock.hasBody() || !titleHit(bashBlock.titleH, y) {
		return false
	}
	bashBlock.toggle()
	return true
}

// toggle flips expansion and reports the new state to OnToggle.
func (bashBlock *BashBlock) toggle() {
	bashBlock.Expanded = !bashBlock.Expanded
	if bashBlock.OnToggle != nil {
		bashBlock.OnToggle(bashBlock.Expanded)
	}
}

// CopyText returns "$ command" plus output when present.
func (bashBlock *BashBlock) CopyText() string {
	var sb strings.Builder
	sb.WriteString("$ ")
	sb.WriteString(bashBlock.Command)
	out := strings.TrimRight(bashBlock.Output, "\n")
	if out != "" {
		sb.WriteByte('\n')
		sb.WriteString(out)
	}
	return sb.String()
}

func (bashBlock *BashBlock) hasBody() bool {
	return strings.TrimSpace(bashBlock.Output) != "" || bashBlock.Status == status.ToolError
}

// Draw renders the "$ command" title and, when expanded, the truncated output body.
func (bashBlock *BashBlock) Draw(ctx components.DrawContext) components.Surface {
	th := bashBlock.theme()
	w := ctx.Max.Width
	if w <= 0 {
		w = 40
	}

	titleWrapped := components.WrapSpans(bashBlock.titleSpans(th), w, ctx.Method)
	titleH := len(titleWrapped)
	bashBlock.titleH = titleH

	var bodyLines []components.RichLine
	if bashBlock.Expanded && bashBlock.hasBody() {
		bodyLines = bashBodyLines(bashBlock.Output, th, w-2, ctx.Method)
	}

	h := titleH + len(bodyLines)
	h = max(h, 1)
	s := components.NewSurface(w, h, bashBlock)
	y := 0
	for _, line := range titleWrapped {
		components.PaintSpans(&s, 0, y, line, ctx.Method)
		y++
	}
	for _, line := range bodyLines {
		components.PaintSpans(&s, 2, y, line, ctx.Method)
		y++
	}
	return s
}

// titleSpans builds the "$ command [exit code] [arrow]" header spans.
func (bashBlock *BashBlock) titleSpans(th components.Theme) []components.Span {
	prefixStyle := th.IdentityOrSuccess()
	switch bashBlock.Status {
	case status.ToolError:
		prefixStyle = th.Destructive
	case status.ToolRunning, status.ToolQueued:
		prefixStyle = th.ToolName
	case status.ToolCancelled, status.ToolRejected:
		prefixStyle = th.Muted
	}

	cmdStyle := th.Foreground
	if bashBlock.Status == status.ToolCancelled || bashBlock.Status == status.ToolRejected {
		cmdStyle.Strikethrough = true
	}

	title := []components.Span{
		{Text: chrome.BashPrompt, Style: prefixStyle},
		{Text: bashBlock.Command, Style: cmdStyle},
	}
	if suf := chrome.StatusSuffix(bashBlock.Status); suf != "" {
		title = append(title, components.Span{Text: suf, Style: th.Muted})
	}
	if bashBlock.Status == status.ToolDone && bashBlock.ExitCode != 0 {
		it := xui.Style{Italic: true}
		title = append(
			title,
			components.Span{Text: " (", Style: it},
			components.Span{Text: "exit code: ", Style: it},
			components.Span{
				Text:  strconv.Itoa(bashBlock.ExitCode),
				Style: xui.Style{Italic: true, Fg: th.Destructive.Fg},
			},
			components.Span{Text: ")", Style: it},
		)
	}
	if bashBlock.hasBody() {
		title = append(title, components.Span{Text: chrome.ExpandArrow(bashBlock.Expanded), Style: th.Muted})
	}
	return title
}

func bashBodyLines(output string, th components.Theme, width int, method xui.WidthMethod) []components.RichLine {
	if output == "" {
		return nil
	}
	text := strings.TrimRight(util.ReplaceAll(output, "\r", ""), "\n")
	fg := th.Foreground
	fg.Dim = true
	spans := []components.Span{{Text: text + "\n", Style: fg}}
	if width < 1 {
		width = 1
	}
	return components.WrapSpans(spans, width, method)
}
