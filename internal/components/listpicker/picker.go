// Package listpicker provides a centered, opaque, filterable list overlay.
// Domain packages map their rows into Item; the picker stays UI-only.
package listpicker

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/layout"
	"github.com/pulseaiclub/phi/internal/util"
)

// Item is one row. Callers own domain mapping (sessions, extensions, …).
type Item struct {
	ID       string // value returned on accept
	Leading  string // left column (e.g. relative time)
	Primary  string // bold column (e.g. short id)
	Detail   string // remaining text
	Badge    string // optional tag after primary (e.g. "current")
	Keywords string // extra filter haystack (full id, aliases, …)
}

// ShowConfig customizes chrome copy for one opening.
type ShowConfig struct {
	Title          string // default "Select"
	FilterHint     string // placeholder when query empty
	Empty          string // no items at all
	EmptyFilter    string // no matches; may contain %q for the query
	Hint           string // bottom border caption
	ConfirmMessage string // replaces the filter prompt while confirming a delete
	ConfirmHint    string // bottom border caption while confirming; default " y delete · n cancel "
	LeadingWidth   int    // fixed leading column; 0 = auto from content / 10
	PrimaryWidth   int    // fixed primary column; 0 = 8
}

// Picker is a filterable list overlay. Enter accepts the selection.
// Ctrl+X arms deletion of the selected row; y/Enter fires OnDelete, n/Esc
// cancels (crush-style in-place confirm — the overlay stays open either way).
type Picker struct {
	Open     bool
	Query    string
	Cursor   int
	Items    []Item
	Selected int
	Theme    components.Theme
	MaxItems int
	Width    int

	OnAccept func(Item)
	OnDelete func(Item)
	OnClose  func()
	FocusReturn components.Widget

	cfg      ShowConfig
	filtered []int
	scroll   int
	confirming bool
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

// Show opens the picker with items. cfg supplies titles/empty copy.
func (p *Picker) Show(items []Item, cfg ShowConfig) {
	p.Open = true
	p.Items = append([]Item(nil), items...)
	p.cfg = normalizeConfig(cfg)
	p.Query = ""
	p.Cursor = 0
	p.Selected = 0
	p.scroll = 0
	p.refilter()
	p.selectPreferred()
}

func normalizeConfig(cfg ShowConfig) ShowConfig {
	if cfg.Title == "" {
		cfg.Title = "Select"
	}
	if cfg.FilterHint == "" {
		cfg.FilterHint = "filter…"
	}
	if cfg.Empty == "" {
		cfg.Empty = "No items"
	}
	if cfg.EmptyFilter == "" {
		cfg.EmptyFilter = "No matches for %q"
	}
	if cfg.Hint == "" {
		cfg.Hint = chrome.ListHint("select")
	}
	if cfg.ConfirmMessage == "" {
		cfg.ConfirmMessage = "Delete?"
	}
	if cfg.ConfirmHint == "" {
		cfg.ConfirmHint = " y delete" + chrome.Sep + "n cancel "
	}
	if cfg.LeadingWidth <= 0 {
		cfg.LeadingWidth = 10
	}
	if cfg.PrimaryWidth <= 0 {
		cfg.PrimaryWidth = 8
	}
	return cfg
}

// Hide closes the picker.
func (p *Picker) Hide() {
	p.Open = false
	p.Query = ""
	p.Cursor = 0
	p.filtered = nil
	p.confirming = false
	if p.OnClose != nil {
		p.OnClose()
	}
}

// SetItems replaces the rows of an open picker in place, keeping the filter
// query. Selection clamps to the new filtered bounds; callers use this after
// deleting a row instead of reopening via Show, which would reset the query.
func (p *Picker) SetItems(items []Item) {
	p.Items = append([]Item(nil), items...)
	p.refilter()
}

func (p *Picker) returnFocus(ctx *components.EventContext) {
	if p.FocusReturn != nil {
		ctx.RequestFocus(p.FocusReturn)
	}
}

func (p *Picker) refilter() {
	p.filtered = p.filtered[:0]
	q := strings.ToLower(strings.TrimSpace(p.Query))
	for i, item := range p.Items {
		if q == "" || itemMatches(item, q) {
			p.filtered = append(p.filtered, i)
		}
	}
	if p.Selected >= len(p.filtered) {
		p.Selected = len(p.filtered) - 1
	}
	if p.Selected < 0 {
		p.Selected = 0
	}
}

func itemMatches(item Item, q string) bool {
	hay := strings.ToLower(strings.Join([]string{
		item.ID, item.Leading, item.Primary, item.Detail, item.Badge, item.Keywords,
	}, " "))
	return strings.Contains(hay, q)
}

func (p *Picker) selectPreferred() {
	if strings.TrimSpace(p.Query) != "" {
		return
	}
	for i, fi := range p.filtered {
		if p.Items[fi].Badge != "" {
			p.Selected = i
			return
		}
	}
}

func (p *Picker) selectedItem() (Item, bool) {
	if p.Selected < 0 || p.Selected >= len(p.filtered) {
		return Item{}, false
	}
	idx := p.filtered[p.Selected]
	if idx < 0 || idx >= len(p.Items) {
		return Item{}, false
	}
	return p.Items[idx], true
}

func (p *Picker) accept() {
	item, ok := p.selectedItem()
	if !ok {
		return
	}
	if p.OnAccept != nil {
		p.OnAccept(item)
	}
	p.Hide()
}

func (p *Picker) insert(text string) {
	p.Query = p.Query[:p.Cursor] + text + p.Query[p.Cursor:]
	p.Cursor += len(text)
	p.Selected = 0
	p.refilter()
}

// Handle drives filter editing, navigation, accept, and close.
func (p *Picker) Handle(ctx *components.EventContext, ev xui.Event) {
	if !p.Open {
		return
	}
	switch e := ev.(type) {
	case xui.KeyEvent:
		if !e.Press {
			return
		}
		p.handleKey(ctx, e)
	case xui.PasteEvent:
		text := util.ReplaceAll(e.Text, "\n", " ")
		text = util.ReplaceAll(text, "\r", " ")
		p.insert(text)
		ctx.ConsumeAndRedraw()
	case xui.MouseEvent:
		if e.Action == xui.MousePress && e.Button == xui.MouseLeft {
			ctx.ConsumeAndRedraw()
		}
	}
}

func (p *Picker) handleKey(ctx *components.EventContext, e xui.KeyEvent) {
	if p.confirming {
		p.handleConfirmKey(ctx, e)
		return
	}
	switch e.Code {
	case xui.KeyEscape:
		p.Hide()
		p.returnFocus(ctx)
		ctx.ConsumeAndRedraw()
	case xui.KeyEnter:
		p.accept()
		p.returnFocus(ctx)
		ctx.ConsumeAndRedraw()
	case xui.KeyUp:
		if p.Selected > 0 {
			p.Selected--
		}
		ctx.ConsumeAndRedraw()
	case xui.KeyDown:
		if p.Selected < len(p.filtered)-1 {
			p.Selected++
		}
		ctx.ConsumeAndRedraw()
	case xui.KeyTab:
		// Tab picks the highlighted row, like Enter. There is no composer text to
		// complete here, and Up / Down / Ctrl+P / Ctrl+N already navigate.
		p.accept()
		p.returnFocus(ctx)
		ctx.ConsumeAndRedraw()
	case xui.KeyBackspace:
		if p.Cursor > 0 {
			_, size := utf8.DecodeLastRuneInString(p.Query[:p.Cursor])
			p.Query = p.Query[:p.Cursor-size] + p.Query[p.Cursor:]
			p.Cursor -= size
			p.Selected = 0
			p.refilter()
		}
		ctx.ConsumeAndRedraw()
	case xui.KeyLeft:
		if p.Cursor > 0 {
			_, size := utf8.DecodeLastRuneInString(p.Query[:p.Cursor])
			p.Cursor -= size
		}
		ctx.ConsumeAndRedraw()
	case xui.KeyRight:
		if p.Cursor < len(p.Query) {
			_, size := utf8.DecodeRuneInString(p.Query[p.Cursor:])
			p.Cursor += size
		}
		ctx.ConsumeAndRedraw()
	case xui.KeyHome:
		p.Cursor = 0
		ctx.ConsumeAndRedraw()
	case xui.KeyEnd:
		p.Cursor = len(p.Query)
		ctx.ConsumeAndRedraw()
	case xui.KeyRune:
		if e.Mods.Has(xui.ModCtrl) {
			switch e.Rune {
			case 'n', 'N':
				if p.Selected < len(p.filtered)-1 {
					p.Selected++
				}
				ctx.ConsumeAndRedraw()
			case 'p', 'P':
				if p.Selected > 0 {
					p.Selected--
				}
				ctx.ConsumeAndRedraw()
			case 'x', 'X':
				// Arm delete-confirm only for callers that own a delete path;
				// other picker domains (branches, …) keep Ctrl+X inert.
				if p.OnDelete != nil {
					p.confirming = true
					ctx.ConsumeAndRedraw()
				}
			}
			return
		}
		if e.Mods.Has(xui.ModAlt) {
			return
		}
		if e.Rune >= 0x20 {
			p.insert(string(e.Rune))
			ctx.ConsumeAndRedraw()
		}
	default:
		ctx.Consume = true
	}
}

// handleConfirmKey drives delete-confirm mode: y/Enter fires OnDelete on the
// selected row, n/Esc disarms. Everything else is swallowed so the filter
// cannot drift while a destructive action is pending.
func (p *Picker) handleConfirmKey(ctx *components.EventContext, e xui.KeyEvent) {
	plainRune := func(r rune) bool {
		return e.Code == xui.KeyRune && !e.Mods.Has(xui.ModCtrl) && !e.Mods.Has(xui.ModAlt) && e.Rune == r
	}
	confirm := e.Code == xui.KeyEnter || plainRune('y') || plainRune('Y')
	cancel := e.Code == xui.KeyEscape || plainRune('n') || plainRune('N')
	switch {
	case confirm:
		p.confirming = false
		if item, ok := p.selectedItem(); ok && p.OnDelete != nil {
			p.OnDelete(item)
		}
	case cancel:
		p.confirming = false
	}
	ctx.ConsumeAndRedraw()
}

// Draw renders the bordered list panel.
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
	if p.filtered == nil {
		p.refilter()
	}

	boxW := p.panelWidth(maxW)
	visible, boxH := p.panelHeight(maxH)
	p.syncScroll(visible)

	panel := components.NewSurface(boxW, boxH, p)
	fillStyle := xui.Style{Fg: th.Foreground.Fg}
	for y := range boxH {
		for x := range boxW {
			panel.SetCell(x, y, xui.Cell{Char: " ", Width: 1, Style: fillStyle})
		}
	}
	borderSt := th.Border
	titleSt := chrome.PanelTitle(th)
	if p.confirming {
		borderSt = th.Destructive
		titleSt.Fg = th.Destructive.Fg
		titleSt.Bold = true
	}
	layout.DrawRoundedBorder(&panel, layout.BorderRounded, borderSt, nil, nil, nil, nil, ctx.Method)

	title := " " + p.cfg.Title + " · " + strconv.Itoa(len(p.Items)) + " "
	if n := len(p.filtered); n != len(p.Items) {
		title = " " + p.cfg.Title + " · " + strconv.Itoa(n) + "/" + strconv.Itoa(len(p.Items)) + " "
	}
	tw := xui.StringWidth(title, ctx.Method)
	tx := max((boxW-tw)/2, 1)
	panel.Print(tx, 0, title, titleSt, ctx.Method)

	p.drawPrompt(&panel, ctx, th, boxW)
	p.drawRows(&panel, ctx, th, boxW, visible)
	p.drawHint(&panel, ctx, th, boxW, boxH)

	ox := max((maxW-boxW)/2, 0)
	oy := max((maxH-boxH)/3, 1)
	out.Children = []components.SubSurface{{
		Origin:  components.Point{X: ox, Y: oy},
		Surface: panel,
		Z:       10,
	}}
	return out
}

func (p *Picker) panelWidth(maxW int) int {
	boxW := p.Width
	if boxW <= 0 {
		// Session previews need room; prefer ~90% of the terminal up to a soft cap.
		boxW = maxW * 9 / 10
		boxW = min(boxW, 112)
		if boxW < 56 {
			boxW = min(maxW, 72)
		}
	}
	if boxW > maxW-2 {
		boxW = maxW - 2
		if boxW < 20 {
			boxW = maxW
		}
	}
	return boxW
}

func (p *Picker) panelHeight(maxH int) (visible, boxH int) {
	maxVisible := p.maxItems()
	avail := max(maxH-5, 3)
	if maxVisible > avail {
		maxVisible = avail
	}
	n := max(len(p.filtered), 1)
	visible = min(n, maxVisible)
	boxH = visible + 3
	if boxH > maxH-2 {
		boxH = maxH - 2
		visible = boxH - 3
		if visible < 1 {
			visible = 1
			boxH = 4
		}
	}
	return visible, boxH
}

func (p *Picker) syncScroll(visible int) {
	if p.Selected < p.scroll {
		p.scroll = p.Selected
	}
	if p.Selected >= p.scroll+visible {
		p.scroll = p.Selected - visible + 1
	}
	maxScroll := max(len(p.filtered)-visible, 0)
	if p.scroll > maxScroll {
		p.scroll = maxScroll
	}
	if p.scroll < 0 {
		p.scroll = 0
	}
}

func (p *Picker) drawPrompt(panel *components.Surface, ctx components.DrawContext, th components.Theme, boxW int) {
	const y = 1
	if p.confirming {
		// The filter is frozen while a delete is pending; its line becomes the
		// destructive question so no extra row is needed in the box layout.
		msg := layout.TruncateToWidth(p.cfg.ConfirmMessage, max(boxW-3, 1), ctx.Method)
		panel.Print(1, y, msg, th.Destructive, ctx.Method)
		return
	}
	panel.Print(1, y, chrome.FilterPrompt, th.Foreground, ctx.Method)
	avail := max(boxW-5, 1)
	q := p.Query
	placeholder := false
	if q == "" {
		q = p.cfg.FilterHint
		placeholder = true
	}
	st := th.Foreground
	if placeholder {
		st = th.Muted
	}
	panel.Print(3, y, layout.TruncateToWidth(q, avail, ctx.Method), st, ctx.Method)
	if !placeholder {
		curCol := xui.StringWidth(p.Query[:min(p.Cursor, len(p.Query))], ctx.Method)
		curCol = max(min(curCol, avail-1), 0)
		panel.Cursor = &components.Point{X: 3 + curCol, Y: y}
	} else {
		panel.Cursor = &components.Point{X: 3, Y: y}
	}
}

func (p *Picker) drawRows(
	panel *components.Surface,
	ctx components.DrawContext,
	th components.Theme,
	boxW, visible int,
) {
	const listY = 2
	if len(p.filtered) == 0 {
		msg := p.cfg.Empty
		if q := strings.TrimSpace(p.Query); q != "" {
			msg = strings.ReplaceAll(p.cfg.EmptyFilter, "%q", strconv.Quote(q))
		}
		panel.Print(2, listY, layout.TruncateToWidth(msg, boxW-3, ctx.Method), th.Muted, ctx.Method)
		return
	}
	for i := range visible {
		ri := i + p.scroll
		if ri < 0 || ri >= len(p.filtered) {
			break
		}
		p.drawRow(panel, ctx, th, boxW, listY+i, ri)
	}
}

func (p *Picker) drawRow(
	panel *components.Surface,
	ctx components.DrawContext,
	th components.Theme,
	boxW, y, ri int,
) {
	item := p.Items[p.filtered[ri]]
	sel := ri == p.Selected
	base := th.Foreground
	muted := th.Muted
	if sel {
		bg := th.SelectionBg.Bg
		for x := 1; x < boxW-1; x++ {
			panel.SetCell(x, y, xui.Cell{Char: " ", Width: 1, Style: xui.Style{Bg: bg}})
		}
		fg := th.SelectionFg
		fg.Bg = bg
		base = fg
		muted = fg
	}

	x := 2
	leadW := p.cfg.LeadingWidth
	if item.Leading != "" {
		panel.Print(x, y, layout.TruncateToWidth(item.Leading, leadW, ctx.Method), muted, ctx.Method)
	}
	x += leadW

	primW := p.cfg.PrimaryWidth
	idSt := base
	idSt.Bold = true
	panel.Print(x, y, layout.TruncateToWidth(item.Primary, primW, ctx.Method), idSt, ctx.Method)
	x += primW + 2

	if item.Badge != "" {
		tag := "· " + item.Badge + "  "
		tagSt := th.Success
		if sel {
			tagSt = base
			tagSt.Bold = true
		}
		panel.Print(x, y, tag, tagSt, ctx.Method)
		x += xui.StringWidth(tag, ctx.Method)
	}
	detail := strings.TrimSpace(item.Detail)
	if detail == "" {
		detail = "—"
	}
	avail := max(boxW-1-x, 1)
	panel.Print(x, y, layout.TruncateToWidth(detail, avail, ctx.Method), base, ctx.Method)
}

func (p *Picker) drawHint(
	panel *components.Surface,
	ctx components.DrawContext,
	th components.Theme,
	boxW, boxH int,
) {
	hint := p.cfg.Hint
	if p.confirming {
		hint = p.cfg.ConfirmHint
	} else if xui.StringWidth(hint, ctx.Method) > boxW-2 {
		hint = chrome.ListHintShort("select")
	}
	y := boxH - 1
	w := xui.StringWidth(hint, ctx.Method)
	x := max((boxW-w)/2, 1)
	panel.Print(x, y, layout.TruncateToWidth(hint, boxW-2, ctx.Method), th.Muted, ctx.Method)
}
