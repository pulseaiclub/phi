package block

import (
	"strings"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
)

// CompactionBlock is a transcript marker shown after context compaction
// (Cursor-style italic "Compacted").
//
// The summary is the only record of what the cut dropped: the summarized
// messages stay on disk but stop reaching the model. The marker therefore
// expands to show the text the model still reads in their place.
type CompactionBlock struct {
	Theme components.Theme
	// TokensBefore is the context size before the cut; 0 hides the count.
	TokensBefore int
	// Summary is the handoff text that replaced the summarized history. Empty
	// hides the expansion arrow.
	Summary  string
	Expanded bool

	// OnToggle is called when the user expands/collapses (click title / Enter).
	OnToggle func(expanded bool)

	titleH int // title row count; only the title responds to ClickAt
}

func (b *CompactionBlock) theme() components.Theme {
	if b.Theme.Muted.Fg.Kind == 0 && b.Theme.Foreground.Fg.Kind == 0 {
		return components.DefaultTheme()
	}
	return b.Theme
}

// hasBody reports whether there is a summary to reveal.
func (b *CompactionBlock) hasBody() bool {
	return strings.TrimSpace(b.Summary) != ""
}

// Handle toggles expansion on Enter/space. Mouse clicks arrive through ClickAt.
func (b *CompactionBlock) Handle(ctx *components.EventContext, ev xui.Event) {
	if !b.hasBody() || !toggleKey(ev) {
		return
	}
	b.toggle()
	ctx.ConsumeAndRedraw()
}

// ClickAt toggles when the click lands on the title row; the summary body stays
// selectable for copy-on-select.
func (b *CompactionBlock) ClickAt(_, y int) bool {
	if !b.hasBody() || !titleHit(b.titleH, y) {
		return false
	}
	b.toggle()
	return true
}

// toggle flips expansion and reports the new state to OnToggle.
func (b *CompactionBlock) toggle() {
	b.Expanded = !b.Expanded
	if b.OnToggle != nil {
		b.OnToggle(b.Expanded)
	}
}

// label is the collapsed marker text, shared by Draw and CopyText.
func (b *CompactionBlock) label() string {
	if b.TokensBefore > 0 {
		return "Compacted from " + components.FormatTokens(b.TokensBefore) + " tokens"
	}
	return "Compacted"
}

// CopyText returns the marker line plus the full summary, mirroring the bash
// block's command + output shape. The summary is not truncated: the expanded
// view has no line cap either.
func (b *CompactionBlock) CopyText() string {
	summary := strings.TrimRight(b.Summary, "\n")
	if summary == "" {
		return b.label()
	}
	return b.label() + "\n" + summary
}

// Draw renders the italic marker line and, when expanded, the summary below it.
func (b *CompactionBlock) Draw(ctx components.DrawContext) components.Surface {
	th := b.theme()
	w := ctx.Max.Width
	if w <= 0 {
		w = 40
	}

	titleStyle := th.Muted
	titleStyle.Italic = true
	titleStyle.Dim = true
	title := []components.Span{{Text: b.label(), Style: titleStyle}}
	if b.hasBody() {
		title = append(title, components.Span{Text: chrome.ExpandArrow(b.Expanded), Style: th.Muted})
	}
	titleLines := components.WrapSpans(title, w, ctx.Method)
	b.titleH = max(len(titleLines), 1)

	var bodyLines []components.RichLine
	if b.Expanded && b.hasBody() {
		bodyLines = dimTextLines(b.Summary, th, w-2, ctx.Method)
	}

	s := components.NewSurface(w, max(b.titleH+len(bodyLines), 1), b)
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
