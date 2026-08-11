package block

import (
	"fmt"
	"strings"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/xui"
)

// BashStatus mirrors bash tool status.
type BashStatus int

const (
	BashDone BashStatus = iota
	BashRunning
	BashError
	BashCancelled
	BashRejected
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
	Status   BashStatus
	ExitCode int
	Expanded bool
	Theme    components.Theme

	// OnToggle is called when the user expands/collapses (click title / Enter).
	OnToggle func(expanded bool)

	titleH int // title row count; body clicks don't toggle (allow selection)
}

func (bashBlock *BashBlock) theme() components.Theme {
	if bashBlock.Theme.Success.Fg.Kind == 0 && bashBlock.Theme.Foreground.Fg.Kind == 0 {
		return components.DefaultTheme()
	}
	return bashBlock.Theme
}

func (bashBlock *BashBlock) Handle(ctx *components.EventContext, ev xui.Event) {
	switch e := ev.(type) {
	case xui.KeyEvent:
		if e.Code == xui.KeyEnter || (e.Code == xui.KeyRune && e.Rune == ' ') {
			if bashBlock.hasBody() {
				bashBlock.toggle(ctx)
			}
		}
	case xui.MouseEvent:
		if e.Action != xui.MousePress || e.Button != xui.MouseLeft {
			return
		}
		// Only the title toggles expand; body stays selectable for copy-on-select.
		if bashBlock.hasBody() && e.Y >= 0 && e.Y < bashBlock.titleH {
			bashBlock.toggle(ctx)
		}
	}
}

// toggle flips expansion, notifies OnToggle, and schedules a redraw.
func (bashBlock *BashBlock) toggle(ctx *components.EventContext) {
	bashBlock.Expanded = !bashBlock.Expanded
	if bashBlock.OnToggle != nil {
		bashBlock.OnToggle(bashBlock.Expanded)
	}
	ctx.ConsumeAndRedraw()
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
	return strings.TrimSpace(bashBlock.Output) != "" || (bashBlock.Status == BashError)
}

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
	if h < 1 {
		h = 1
	}
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
	prefixStyle := th.Success
	switch bashBlock.Status {
	case BashError:
		prefixStyle = th.Destructive
	case BashRunning:
		prefixStyle = th.ToolName
	case BashCancelled, BashRejected:
		prefixStyle = th.Muted
	}

	cmdStyle := th.Foreground
	if bashBlock.Status == BashCancelled || bashBlock.Status == BashRejected {
		cmdStyle.Strikethrough = true
	}

	title := []components.Span{
		{Text: "$ ", Style: prefixStyle},
		{Text: bashBlock.Command, Style: cmdStyle},
	}
	switch bashBlock.Status {
	case BashCancelled:
		title = append(title, components.Span{Text: " (cancelled)", Style: th.Muted})
	case BashRejected:
		title = append(title, components.Span{Text: " (rejected)", Style: th.Muted})
	}
	if bashBlock.Status == BashDone && bashBlock.ExitCode != 0 {
		it := xui.Style{Italic: true}
		title = append(title,
			components.Span{Text: " (", Style: it},
			components.Span{Text: "exit code: ", Style: it},
			components.Span{Text: fmt.Sprintf("%d", bashBlock.ExitCode), Style: xui.Style{Italic: true, Fg: th.Destructive.Fg}},
			components.Span{Text: ")", Style: it},
		)
	}
	if bashBlock.hasBody() {
		arrow := " ▶"
		if bashBlock.Expanded {
			arrow = " ▼"
		}
		title = append(title, components.Span{Text: arrow, Style: th.Muted})
	}
	return title
}

func bashBodyLines(output string, th components.Theme, width int, method xui.WidthMethod) []components.RichLine {
	if output == "" {
		return nil
	}
	text := strings.TrimRight(strings.ReplaceAll(output, "\r", ""), "\n")
	fg := th.Foreground
	fg.Dim = true
	spans := []components.Span{{Text: text + "\n", Style: fg}}
	if width < 1 {
		width = 1
	}
	return components.WrapSpans(spans, width, method)
}
