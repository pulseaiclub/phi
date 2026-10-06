package mention

import (
	"strings"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/layout"
)

// Item is one row in the mention / slash picker.
type Item struct {
	Path        string // primary label without Prefix (file path or command name)
	Description string // optional secondary hint shown after the label
}

// Picker is a floating list anchored above the composer.
// Query lives in the composer; this widget only navigates and accepts.
type Picker struct {
	Open     bool
	Items    []Item
	Selected int
	Status   string // empty-state / error message when Items is empty
	Theme    components.Theme
	MaxItems int // visible rows; default 12
	Width    int // panel width; 0 = fill anchor
	// Prefix is drawn before Path (default "@"). Use "/" for slash commands.
	Prefix string
	// NoPrefix suppresses the default "@" when Prefix is empty (shortcut help list).
	NoPrefix bool
	// OnAccept applies the highlighted item on Enter. It may execute something
	// (accepting `/new` submits it).
	OnAccept func(Item)
	// OnComplete applies the highlighted item on Tab, which must never execute
	// anything. nil falls back to OnAccept, which is correct when accept only
	// edits the composer (`@path`, `?` help).
	OnComplete func(Item)
	OnCancel   func()

	// AnchorBottomY is the screen Y of the top edge of the composer.
	// The picker sits just above this row.
	AnchorBottomY int
	AnchorX       int
	AnchorWidth   int
}

func (p *Picker) theme() components.Theme {
	if p.Theme.Border.Fg.Kind == 0 && p.Theme.Foreground.Fg.Kind == 0 {
		return components.DefaultTheme()
	}
	return p.Theme
}

func (p *Picker) maxItems() int {
	if p.MaxItems < 1 {
		return 12
	}
	return p.MaxItems
}

// Show opens the picker (keeps current items/selection unless reset).
func (p *Picker) Show() {
	p.Open = true
	if p.Selected < 0 {
		p.Selected = 0
	}
	p.clampSelected()
}

// Hide closes the picker without invoking OnCancel.
func (p *Picker) Hide() {
	p.Open = false
}

// SetResults replaces the list and resets selection to 0.
func (p *Picker) SetResults(items []Item, status string) {
	p.Items = items
	p.Status = status
	p.Selected = 0
	p.clampSelected()
}

func (p *Picker) clampSelected() {
	if len(p.Items) == 0 {
		p.Selected = 0
		return
	}
	if p.Selected >= len(p.Items) {
		p.Selected = len(p.Items) - 1
	}
	if p.Selected < 0 {
		p.Selected = 0
	}
}

// Accept applies the highlighted item via OnAccept and closes.
// With no items, it still closes so Enter does not leave a stuck overlay.
func (p *Picker) Accept() bool {
	return p.apply(p.OnAccept)
}

// Complete applies the highlighted item via OnComplete (Tab) and closes.
// Enter may run a slash command; Tab only fills the composer, so callers whose
// accept executes register a fill-only OnComplete instead.
func (p *Picker) Complete() bool {
	fn := p.OnComplete
	if fn == nil {
		fn = p.OnAccept
	}
	return p.apply(fn)
}

// apply hides the picker and hands the highlighted item to fn (nil-safe).
// It returns false when there was nothing to apply; the picker closes anyway so
// a stray Enter / Tab cannot leave a stuck overlay.
func (p *Picker) apply(fn func(Item)) bool {
	if !p.Open {
		return false
	}
	if len(p.Items) == 0 || p.Selected < 0 || p.Selected >= len(p.Items) {
		p.Hide()
		return false
	}
	item := p.Items[p.Selected]
	p.Hide()
	if fn != nil {
		fn(item)
	}
	return true
}

// Cancel closes and notifies OnCancel.
func (p *Picker) Cancel() {
	if !p.Open {
		return
	}
	p.Open = false
	if p.OnCancel != nil {
		p.OnCancel()
	}
}

// HandleNav processes navigation / accept / cancel keys.
// Returns true if the event was consumed.
func (p *Picker) HandleNav(ev xui.KeyEvent) bool {
	if !p.Open || !ev.Press {
		return false
	}
	switch ev.Code {
	case xui.KeyEscape:
		p.Cancel()
		return true
	case xui.KeyEnter:
		_ = p.Accept()
		return true
	case xui.KeyUp:
		if p.Selected > 0 {
			p.Selected--
		}
		return true
	case xui.KeyDown:
		if p.Selected < len(p.Items)-1 {
			p.Selected++
		}
		return true
	case xui.KeyTab:
		// Tab completes rather than moves: Up / Down / Ctrl+P / Ctrl+N already
		// cover navigation, and a picker that only walks a list is a dead end.
		// Shift+Tab completes too — previous-row went away with it.
		p.Complete()
		return true
	case xui.KeyRune:
		if ev.Mods.Has(xui.ModCtrl) {
			switch ev.Rune {
			case 'n', 'N':
				if p.Selected < len(p.Items)-1 {
					p.Selected++
				}
				return true
			case 'p', 'P':
				if p.Selected > 0 {
					p.Selected--
				}
				return true
			}
		}
	}
	return false
}

// Handle routes key events to the picker while it is open.
func (p *Picker) Handle(ctx *components.EventContext, ev xui.Event) {
	if !p.Open {
		return
	}
	if e, ok := ev.(xui.KeyEvent); ok {
		if p.HandleNav(e) {
			ctx.ConsumeAndRedraw()
		}
	}
}

// Draw renders the picker as a full-screen overlay with a panel child.
func (p *Picker) Draw(ctx components.DrawContext) components.Surface {
	th := p.theme()
	maxW, maxH := ctx.Max.Width, ctx.Max.Height
	if maxW <= 0 {
		maxW = 80
	}
	if maxH <= 0 {
		maxH = 24
	}
	out := components.Surface{Size: components.Size{Width: maxW, Height: maxH}, Widget: p}
	if !p.Open {
		return out
	}

	boxW := p.Width
	if boxW <= 0 {
		boxW = p.AnchorWidth
	}
	if boxW <= 0 {
		boxW = maxW * 3 / 4
	}
	if boxW > maxW-2 {
		boxW = maxW - 2
	}
	if boxW < 24 {
		boxW = maxW
		boxW = min(boxW, 60)
	}

	maxVisible := p.maxItems()
	availAbove := p.AnchorBottomY - 1
	availAbove = max(availAbove, 3)
	if maxVisible > availAbove-2 { // borders
		maxVisible = availAbove - 2
	}
	if maxVisible < 1 {
		maxVisible = 1
	}

	nItems := len(p.Items)
	visible := nItems
	if visible == 0 {
		visible = 1 // status row
	}
	if visible > maxVisible {
		visible = maxVisible
	}

	boxH := 2 + visible // borders + rows
	if boxH > availAbove {
		boxH = availAbove
		visible = boxH - 2
		if visible < 1 {
			visible = 1
			boxH = 3
		}
	}

	scroll := 0
	if nItems > 0 && p.Selected >= visible {
		scroll = p.Selected - visible + 1
	}
	if scroll < 0 {
		scroll = 0
	}
	if nItems > 0 && scroll > nItems-visible {
		scroll = nItems - visible
		scroll = max(scroll, 0)
	}

	panel := components.NewSurface(boxW, boxH, p)
	fillStyle := xui.Style{Fg: th.Foreground.Fg}
	for y := 0; y < boxH; y++ {
		for x := 0; x < boxW; x++ {
			panel.SetCell(x, y, xui.Cell{Char: " ", Width: 1, Style: fillStyle})
		}
	}
	layout.DrawRoundedBorder(&panel, layout.BorderRounded, th.Border, nil, nil, nil, nil, ctx.Method)

	// Soft blue selection bar (distinct from palette yellow).
	selBg := xui.RGBColor(0x3a, 0x5a, 0x7a)

	padL := 1
	listY := 1
	prefix := p.Prefix
	if prefix == "" && !p.NoPrefix {
		prefix = "@"
	}
	if nItems == 0 {
		msg := p.Status
		if msg == "" {
			msg = "No matches"
		}
		panel.Print(padL+1, listY, layout.TruncateToWidth(msg, boxW-3, ctx.Method), th.Muted, ctx.Method)
	} else {
		pathSt := th.ToolName
		descSt := th.Muted
		innerW := boxW - 3
		const minDescW = 8
		gap := "  "
		gapW := xui.StringWidth(gap, ctx.Method)
		// Align every hint into one column so the list reads top-down instead of
		// zig-zag. Capping the column keeps one long label from squeezing the
		// hints to nothing: rows past the cap just get a shorter hint.
		colW := 0
		for _, it := range p.Items {
			if it.Description == "" {
				continue
			}
			colW = max(colW, xui.StringWidth(prefix+it.Path, ctx.Method))
		}
		colW = min(colW, innerW-gapW-minDescW)
		colW = max(colW, 0)
		for row := 0; row < visible; row++ {
			fi := row + scroll
			if fi < 0 || fi >= nItems {
				break
			}
			item := p.Items[fi]
			sel := fi == p.Selected
			y := listY + row
			label := prefix + item.Path
			st := pathSt
			dst := descSt
			if sel {
				for x := 1; x < boxW-1; x++ {
					panel.SetCell(x, y, xui.Cell{Char: " ", Width: 1, Style: xui.Style{Bg: selBg}})
				}
				st = xui.Style{Fg: xui.RGBColor(0xe0, 0xf0, 0xff), Bg: selBg, Bold: true}
				dst = xui.Style{Fg: xui.RGBColor(0xb0, 0xc8, 0xe0), Bg: selBg}
			}
			labelW := xui.StringWidth(label, ctx.Method)
			descBudget := innerW - max(labelW, colW) - gapW
			if item.Description == "" || descBudget < minDescW {
				panel.Print(padL+1, y, layout.TruncateToWidth(label, innerW, ctx.Method), st, ctx.Method)
				continue
			}
			panel.Print(padL+1, y, label, st, ctx.Method)
			pad := strings.Repeat(" ", max(colW-labelW, 0))
			panel.Print(padL+1+labelW, y, gap+pad, dst, ctx.Method)
			panel.Print(
				padL+1+labelW+gapW+len(pad),
				y,
				layout.TruncateToWidth(item.Description, descBudget, ctx.Method),
				dst,
				ctx.Method,
			)
		}
	}

	ox := p.AnchorX
	ox = max(ox, 0)
	if ox+boxW > maxW {
		ox = maxW - boxW
		ox = max(ox, 0)
	}
	oy := p.AnchorBottomY - boxH
	oy = max(oy, 0)

	out.Children = []components.SubSurface{{
		Origin:  components.Point{X: ox, Y: oy},
		Surface: panel,
		Z:       15,
	}}
	return out
}
