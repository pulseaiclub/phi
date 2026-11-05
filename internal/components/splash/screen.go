package splash

import (
	"strings"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
)

// Screen is the splash screen: animated sphere + intro copy.
// Shown when the transcript is empty at startup.
type Screen struct {
	Sphere *Sphere
	Theme  components.Theme
	// Brand is the product name in the hero line (default "phi").
	Brand string
	Hint  string // optional tip under the help line; empty uses the default
}

func (w *Screen) brand() string {
	if w.Brand == "" {
		return "Phi"
	}
	return w.Brand
}

func (w *Screen) hintLines() []string {
	if w.Hint != "" {
		return wrapHint(w.Hint, 48)
	}
	return []string{
		"Type a message below and press Enter to start",
	}
}

func wrapHint(s string, width int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	var cur string
	for _, word := range words {
		if cur == "" {
			cur = word
			continue
		}
		if len(cur)+1+len(word) > width {
			lines = append(lines, cur)
			cur = word
			continue
		}
		cur += " " + word
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// Handle forwards events to the animated sphere.
func (w *Screen) Handle(ctx *components.EventContext, ev xui.Event) {
	if w.Sphere != nil {
		w.Sphere.Handle(ctx, ev)
	}
}

// Draw renders the centered sphere plus brand, help, and hint copy.
func (w *Screen) Draw(ctx components.DrawContext) components.Surface {
	maxW, maxH := ctx.Max.Width, ctx.Max.Height
	if maxW <= 0 {
		maxW = 80
	}
	if maxH <= 0 {
		maxH = 24
	}
	root := components.Surface{Size: components.Size{Width: maxW, Height: maxH}, Widget: w}

	th := w.Theme
	if th == (components.Theme{}) {
		th = components.DefaultTheme()
	}

	sphere := w.Sphere
	// The sphere tints itself from the theme, so a live theme switch repaints
	// the hero without rebuilding the widget.
	sphere.Theme = th
	// Fit sphere into available space; default is 40×40.
	sphereSize := 40
	if maxH < sphereSize+2 {
		sphereSize = maxH - 2
	}
	if sphereSize > maxW/2 {
		sphereSize = maxW / 2
	}
	if sphereSize < 12 {
		sphereSize = 12
	}
	sphere.Width, sphere.Height = sphereSize, sphereSize
	sphereSurf := sphere.Draw(
		ctx.WithConstraints(components.Size{}, components.Size{Width: sphereSize, Height: sphereSize}),
	)

	const gap = 2
	textW := maxW - sphereSize - gap - 4
	textW = max(textW, 20)
	textW = min(textW, 50)

	// Brand near-white; only Ctrl+K / ? carry the accent punch.
	brand := xui.Style{Fg: xui.RGBColor(0xe8, 0xec, 0xf2), Bold: true}
	if th.Foreground.Fg.Kind == xui.ColorRGB {
		brand = xui.Style{Fg: th.Foreground.Fg, Bold: true}
	}
	helpKey := th.ToolName
	if helpKey == (xui.Style{}) {
		helpKey = th.IdentityOrSuccess()
	}
	if helpKey == (xui.Style{}) {
		helpKey = xui.Style{Fg: xui.RGBColor(0x7d, 0xc3, 0xff), Bold: true}
	}
	// Secondary copy: theme muted without Dim. ANSI Dim + bright-black
	// (Terminal IndexedColor 8) is nearly invisible on dark backgrounds.
	body := splashBodyStyle(th)

	lines := []struct {
		spans []components.Span
	}{
		{spans: []components.Span{{Text: w.brand(), Style: brand}}},
		{spans: []components.Span{{Text: "terminal coding agent", Style: body}}},
		{spans: nil}, // blank
		{spans: []components.Span{
			{Text: "Ctrl+K", Style: helpKey},
			{Text: " command palette", Style: body},
			{Text: ", ", Style: body},
			{Text: "?", Style: helpKey},
			{Text: " shortcuts", Style: body},
		}},
	}
	for _, h := range w.hintLines() {
		lines = append(lines, struct{ spans []components.Span }{spans: []components.Span{{Text: h, Style: body}}})
	}

	textH := len(lines)
	textH = max(textH, 1)
	textSurf := components.NewSurface(textW, textH, nil)
	for y, line := range lines {
		if line.spans == nil {
			continue
		}
		components.PaintSpans(&textSurf, 0, y, line.spans, ctx.Method)
	}

	blockW := sphereSize + gap + textW
	blockH := sphereSize
	blockH = max(blockH, textH)
	ox := (maxW - blockW) / 2
	oy := (maxH - blockH) / 2
	if ox < 0 {
		ox = 0
	}
	if oy < 0 {
		oy = 0
	}
	textOY := oy + (blockH-textH)/2
	textOY = max(textOY, 0)

	root.Children = []components.SubSurface{
		{Origin: components.Point{X: ox, Y: oy}, Surface: sphereSurf},
		{Origin: components.Point{X: ox + sphereSize + gap, Y: textOY}, Surface: textSurf},
	}
	return root
}

// splashBodyStyle is readable secondary copy for the hero — theme muted
// without Dim, lifting ANSI bright-black to a mid gray.
func splashBodyStyle(th components.Theme) xui.Style {
	st := th.Muted
	st.Dim = false
	if st.Fg.Kind == xui.ColorIndex && st.Fg.Index <= 8 {
		st.Fg = xui.IndexedColor(245)
	}
	if st.Fg.Kind == 0 {
		st.Fg = xui.RGBColor(0xa8, 0xb2, 0xc0)
	}
	return st
}
