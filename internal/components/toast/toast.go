package toast

import (
	"strings"
	"time"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/layout"
	"github.com/pulseaiclub/phi/internal/components/text"
)

// ToastKind selects border/icon colors for a toast.
type ToastKind int

// Toast kinds select the border and icon colors for a notification.
const (
	ToastSuccess ToastKind = iota
	ToastError
	ToastWarning
)

// Toast is a top overlay notification (green border + ✓ for success).
type Toast struct {
	Message string
	Kind    ToastKind
	Until   time.Time
	Theme   components.Theme
}

// Show displays message for d (default 2s).
func (t *Toast) Show(message string, kind ToastKind, d time.Duration) {
	if d <= 0 {
		d = 2 * time.Second
	}
	t.Message = message
	t.Kind = kind
	t.Until = time.Now().Add(d)
}

// Clear hides the toast immediately.
func (t *Toast) Clear() {
	t.Message = ""
	t.Until = time.Time{}
}

// Visible reports whether the toast should be drawn.
func (t *Toast) Visible() bool {
	return t.Message != "" && time.Now().Before(t.Until)
}

func (t *Toast) theme() components.Theme {
	if t.Theme.Success.Fg.Kind == 0 && t.Theme.Foreground.Fg.Kind == 0 {
		return components.DefaultTheme()
	}
	return t.Theme
}

// Handle is a no-op; toasts do not take input.
func (*Toast) Handle(_ *components.EventContext, _ xui.Event) {}

// Draw paints a full-screen transparent host with the toast bar near the top.
func (t *Toast) Draw(ctx components.DrawContext) components.Surface {
	maxW, maxH := ctx.Max.Width, ctx.Max.Height
	if maxW <= 0 {
		maxW = 80
	}
	if maxH <= 0 {
		maxH = 24
	}
	out := components.Surface{Size: components.Size{Width: maxW, Height: maxH}, Widget: t}
	if !t.Visible() {
		return out
	}

	th := t.theme()
	border, icon := toastChrome(t.Kind, th)
	fg := th.Foreground

	iconWidth := 0
	if icon != "" {
		iconWidth = xui.StringWidth(icon+" ", ctx.Method)
	}
	messageWidth := 0
	for line := range strings.SplitSeq(t.Message, "\n") {
		messageWidth = max(messageWidth, xui.StringWidth(line, ctx.Method))
	}

	// width = max(floor(screen*0.95), 40), shrink to content.
	boxW := maxW * 95 / 100
	if boxW < 40 {
		boxW = maxW
		boxW = min(boxW, 40)
	}
	if boxW > maxW-2 {
		boxW = maxW - 2
		if boxW < 8 {
			boxW = maxW
		}
	}
	boxW = min(maxW, max(8, min(boxW, messageWidth+iconWidth+4)))
	inset := 1
	if boxW < 4 || maxH < 3 {
		inset = 0
	}
	// Leave room for a wide glyph before reserving space for the icon.
	if boxW-2*inset-iconWidth < 2 {
		icon = ""
		iconWidth = 0
	}
	textX := inset + iconWidth
	avail := boxW - inset - textX
	lines := text.WrapEditorLines(t.Message, avail, ctx.Method)
	boxH := min(len(lines)+2*inset, maxH)
	visibleLines := boxH - 2*inset

	panel := components.NewSurface(boxW, boxH, t)
	fill := xui.Style{Fg: fg.Fg}
	for y := range boxH {
		for x := range boxW {
			panel.SetCell(x, y, xui.Cell{Char: " ", Width: 1, Style: fill})
		}
	}
	if inset > 0 {
		layout.DrawRoundedBorder(&panel, layout.BorderRounded, border, nil, nil, nil, nil, ctx.Method)
	}
	if icon != "" {
		panel.Print(inset, inset, icon+" ", border, ctx.Method)
	}
	for y, line := range lines[:visibleLines] {
		if y == visibleLines-1 && len(lines) > visibleLines {
			line = components.TruncateToWidth(line, avail-1, ctx.Method) + "…"
		}
		panel.Print(textX, inset+y, components.TruncateToWidth(line, avail, ctx.Method), fg, ctx.Method)
	}

	ox := (maxW - boxW) / 2
	oy := 1
	if oy+boxH > maxH {
		oy = 0
	}
	if ox < 0 {
		ox = 0
	}
	out.Children = []components.SubSurface{{
		Origin:  components.Point{X: ox, Y: oy},
		Surface: panel,
		Z:       50,
	}}
	return out
}

func toastChrome(kind ToastKind, th components.Theme) (border xui.Style, icon string) {
	switch kind {
	case ToastError:
		return th.Destructive, chrome.Err
	case ToastWarning:
		return th.Warning, ""
	default:
		return th.Success, chrome.Ok
	}
}
