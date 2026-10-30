package toast

import (
	"time"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/layout"
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

	msg := t.Message
	inner := " " + icon
	if icon != "" {
		inner += " "
	}
	inner += msg + " "

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
	tw := xui.StringWidth(inner, ctx.Method) + 2 // + borders
	if tw < boxW {
		boxW = tw
	}
	if boxW < 8 {
		boxW = 8
	}
	boxH := 3
	boxH = min(boxH, maxH)

	panel := components.NewSurface(boxW, boxH, t)
	fill := xui.Style{Fg: fg.Fg}
	for y := 0; y < boxH; y++ {
		for x := 0; x < boxW; x++ {
			panel.SetCell(x, y, xui.Cell{Char: " ", Width: 1, Style: fill})
		}
	}
	layout.DrawRoundedBorder(&panel, layout.BorderRounded, border, nil, nil, nil, nil, ctx.Method)

	textX := 1
	if icon != "" {
		panel.Print(textX, 1, icon+" ", border, ctx.Method)
		textX += xui.StringWidth(icon+" ", ctx.Method)
	}
	avail := boxW - 1 - textX
	avail = max(avail, 1)
	panel.Print(textX, 1, components.TruncateToWidth(msg, avail, ctx.Method), fg, ctx.Method)

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
