// Package listpicker provides a centered, opaque, filterable list overlay.
// Domain packages map their rows into Item; the picker stays UI-only, and the
// one filter line searches those rows.
package listpicker

import (
	"slices"
	"strconv"
	"strings"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/layout"
	"github.com/pulseaiclub/phi/internal/components/textfield"
)

// Item supplies the two display columns, Primary and Detail. Callers own
// domain mapping; column sizing and row styling belong to the picker.
type Item struct {
	ID       string // value returned on accept
	Leading  string // optional marker slot, reserved for the current branch
	Primary  string // bold name column
	Detail   string // core description column
	Keywords string // extra filter haystack (full id, aliases, …)
	Current  bool   // highlight the row as the active choice, without a glyph
}

const (
	columnGap   = 2
	markerWidth = 2
)

// ShowConfig customizes chrome copy for one opening.
type ShowConfig struct {
	Title          string // default "Select"
	FilterHint     string // placeholder when query empty
	Empty          string // no items at all
	EmptyFilter    string // no matches; may contain %q for the query
	Hint           string // bottom border caption
	HintShort      string // compact Hint for narrow panels; default chrome.ListHintShort("select")
	ConfirmMessage string // replaces the filter prompt while confirming a delete
	ConfirmHint    string // bottom border caption while confirming; default " y/enter delete · n/esc cancel "
	ConfirmShort   string // narrow-screen confirmation caption; optional
	PrimaryWidth   int    // maximum name width; 0 = widest name in the list
	TabNavigates   bool   // Tab/Shift+Tab move the highlight; default Tab accepts
	Fuzzy          bool   // name fields also match as a subsequence ("thm" → theme)
	Style          Style
}

// Style controls the visual treatment of a picker without changing its input
// and selection behavior.
type Style uint8

const (
	StyleDefault Style = iota
	StyleCommand
)

// Picker is a filterable list overlay. Enter accepts the selection.
// Ctrl+X arms deletion of the selected row; Y/Enter fires OnDelete, N/Esc
// disarms. The overlay stays open after a delete so callers can remove
// several rows without reopening.
type Picker struct {
	Open     bool
	Items    []Item
	Selected int
	// line is the shared single-line filter buffer; Query()/SetQuery expose it.
	line textfield.Field

	Theme    components.Theme
	MaxItems int
	Width    int

	OnAccept func(Item)
	OnDelete func(Item)
	// OnSave arms the same confirmation path as delete and returns whether the
	// save succeeded. A successful save closes; failure keeps the row selected.
	OnSave      func(Item) bool
	CanSave     func(Item) bool // optional per-row guard before confirmation
	OnEscape    func() bool     // return true to keep the picker open
	OnClose     func()
	FocusReturn components.Widget

	cfg          ShowConfig
	filtered     []hit
	scroll       int
	confirming   bool
	saveConfirm  bool
	confirmID    string // row the pending confirmation was armed on
	cancelAccept bool
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
// Query returns the filter line.
func (p *Picker) Query() string { return p.line.Text() }

// SetQuery replaces the filter line and parks the caret at the end.
func (p *Picker) SetQuery(q string) { p.line.SetText(q) }

func (p *Picker) Show(items []Item, cfg ShowConfig) {
	p.Open = true
	p.Items = append([]Item(nil), items...)
	p.cfg = normalizeConfig(cfg)
	p.line.SetText("")
	p.Selected = 0

	p.scroll = 0
	p.confirming = false
	p.saveConfirm = false
	p.confirmID = ""
	p.cancelAccept = false
	p.refilter()
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
		cfg.ConfirmHint = " y/enter delete" + chrome.Sep + "n/esc cancel "
	}
	return cfg
}

// Hide closes the picker.
func (p *Picker) Hide() {
	p.Open = false
	p.line.SetText("")
	p.filtered = nil

	p.confirming = false
	p.saveConfirm = false
	p.confirmID = ""
	p.cancelAccept = false
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

// State keeps a caller's place when a detail view temporarily replaces the list.
type State struct {
	Query      string
	SelectedID string
}

func (p *Picker) State() State {
	item, _ := p.selectedItem()
	return State{Query: p.Query(), SelectedID: item.ID}
}

func (p *Picker) Restore(state State) {
	p.SetQuery(state.Query)
	p.refilter()

	for i, hit := range p.filtered {
		if p.Items[hit.index].ID == state.SelectedID {
			p.Selected = i
			break
		}
	}
}

func (p *Picker) returnFocus(ctx *components.EventContext) {
	if p.FocusReturn != nil {
		ctx.RequestFocus(p.FocusReturn)
	}
}

// hit is one row that passed the filter, carrying how well it answered the
// query so the best answers sort to the top.
type hit struct {
	index int
	tier  matchTier
}

// matchTier grades a hit by where the query landed. Name fields outrank free
// text: typing a branch name must not sink below a commit subject that happens
// to spell the same thing.
type matchTier int

const (
	tierNone     matchTier = iota // no hit at all
	tierText                      // only Detail / Keywords matched
	tierSubseq                    // name fields match as a subsequence (fuzzy)
	tierContains                  // Primary or ID contains the query
	tierPrefix                    // Primary or ID starts with it
	tierExact                     // Primary or ID is exactly the query
)

func matchTierOf(item Item, q string, fuzzy bool) matchTier {
	primary, id := strings.ToLower(item.Primary), strings.ToLower(item.ID)
	switch {
	case primary == q || id == q:
		return tierExact
	case strings.HasPrefix(primary, q) || strings.HasPrefix(id, q):
		return tierPrefix
	case strings.Contains(primary, q) || strings.Contains(id, q):
		return tierContains
	case fuzzy && nameSubseq(item, q):
		return tierSubseq
	case itemMatches(item, q):
		return tierText
	}
	return tierNone
}

// nameSubseq reports whether q appears in order across the row's name fields:
// "thm" finds "theme". A name-shaped guess outranks a literal match in free
// text, which is what made palette search forgiving before the shared picker.
func nameSubseq(item Item, q string) bool {
	hay := strings.ToLower(item.Primary + " " + item.ID + " " + item.Keywords)
	target := []rune(q)
	i := 0
	for _, r := range hay {
		if i < len(target) && r == target[i] {
			i++
		}
	}
	return i == len(target)
}

func (p *Picker) refilter() {
	q := strings.ToLower(strings.TrimSpace(p.Query()))
	p.filtered = p.filtered[:0]
	for i, item := range p.Items {
		tier := tierText // no query: every row stays, in the caller's order
		if q != "" {
			tier = matchTierOf(item, q, p.cfg.Fuzzy)
		}
		if tier > tierNone {
			p.filtered = append(p.filtered, hit{index: i, tier: tier})
		}
	}
	if q != "" {
		// Best answer under the cursor. Stable, so equally good hits keep the
		// caller's ranking (current branch, recently checked out, newest).
		slices.SortStableFunc(p.filtered, func(a, b hit) int { return int(b.tier) - int(a.tier) })
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
		item.ID, item.Leading, item.Primary, item.Detail, item.Keywords,
	}, " "))
	return strings.Contains(hay, q)
}

// IsConfirming reports whether an action is waiting for confirmation.
func (p *Picker) IsConfirming() bool { return p.confirming }

// CancelAccept keeps the picker open when an adapter opens a nested page or
// rejects a disabled row.
func (p *Picker) CancelAccept() { p.cancelAccept = true }

func (p *Picker) selectedItem() (Item, bool) {
	if p.Selected < 0 || p.Selected >= len(p.filtered) {
		return Item{}, false
	}
	idx := p.filtered[p.Selected].index
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
	p.cancelAccept = false
	if p.OnAccept != nil {
		p.OnAccept(item)
	}
	if p.cancelAccept {
		return
	}
	p.Hide()
}

func (p *Picker) insert(text string) {
	p.line.Insert(text)
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
		if !p.confirming {
			p.insert(e.Text)
		}
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
		if p.OnEscape != nil && p.OnEscape() {
			ctx.ConsumeAndRedraw()
			return
		}
		p.Hide()
		p.returnFocus(ctx)
		ctx.ConsumeAndRedraw()
	case xui.KeyEnter:
		p.accept()
		if !p.Open {
			p.returnFocus(ctx)
		}
		ctx.ConsumeAndRedraw()
	case xui.KeyTab:
		if !p.cfg.TabNavigates {
			// Tab picks the highlighted row, like Enter. There is no composer text
			// to complete here, and Up / Down / Ctrl+P / Ctrl+N already navigate.
			p.accept()
			if !p.Open {
				p.returnFocus(ctx)
			}
			ctx.ConsumeAndRedraw()
			return
		}
		// Enter is the action key here; Tab shifts the highlight, so a stray Tab
		// can never fire the row under the cursor (e.g. an extension unload).
		if e.Mods.Has(xui.ModShift) {
			if p.Selected > 0 {
				p.Selected--
			}
		} else if p.Selected < len(p.filtered)-1 {
			p.Selected++
		}
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
	case xui.KeyBackspace:
		if p.line.DeleteBefore() {
			p.Selected = 0
			p.refilter()
		}
		ctx.ConsumeAndRedraw()
	case xui.KeyLeft:
		p.line.Move(-1)
		ctx.ConsumeAndRedraw()
	case xui.KeyRight:
		p.line.Move(1)
		ctx.ConsumeAndRedraw()
	case xui.KeyHome:
		p.line.Home()
		ctx.ConsumeAndRedraw()
	case xui.KeyEnd:
		p.line.End()
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
				// picker domains without one keep Ctrl+X inert.
				if item, ok := p.selectedItem(); p.OnDelete != nil && ok {
					p.confirming = true
					p.saveConfirm = false
					p.confirmID = item.ID
					ctx.ConsumeAndRedraw()
				}
			case 's', 'S':
				item, selected := p.selectedItem()
				if p.OnSave != nil && selected &&
					(p.CanSave == nil || p.CanSave(item)) {
					p.confirming = true
					p.saveConfirm = true
					p.confirmID = item.ID
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

// handleConfirmKey drives delete-confirm mode: Y/Enter fires OnDelete on the
// selected row, N/Esc disarms. Everything else is swallowed so the filter
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
		// The confirmation names one row. A refresh that moved another row
		// into place (or a CanSave flip) must not act on the wrong item.
		item, ok := p.selectedItem()
		if !ok || item.ID != p.confirmID {
			break
		}
		if p.saveConfirm {
			if p.OnSave != nil && (p.CanSave == nil || p.CanSave(item)) && p.OnSave(item) {
				p.Hide()
				p.returnFocus(ctx)
			}
			break
		}
		if p.OnDelete != nil {
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
	borderSt := th.Muted
	borderSt.Dim = true
	titleSt := chrome.PanelTitle(th)
	if p.confirming {
		borderSt = th.Destructive
		if p.saveConfirm {
			borderSt = chrome.ModalBorder(th)
		}
		titleSt.Fg = th.Destructive.Fg
		titleSt.Bold = true
	}
	layout.DrawRoundedBorder(&panel, layout.BorderSquare, borderSt, nil, nil, nil, nil, ctx.Method)

	label, total := p.cfg.Title, len(p.Items)
	count := strconv.Itoa(total)
	if n := len(p.filtered); n != total {
		count = strconv.Itoa(n) + "/" + strconv.Itoa(total)
	}
	if boxH >= 7 {
		title := components.TruncateToWidth(label+" · "+count, boxW-2, ctx.Method)
		tx := max((boxW-xui.StringWidth(title, ctx.Method))/2, 1)
		panel.Print(tx, 1, title, titleSt, ctx.Method)
	}

	p.drawPrompt(&panel, ctx, th, boxW)
	p.drawRows(&panel, ctx, th, boxW, visible)
	p.drawHint(&panel, ctx, th, boxW, boxH)

	ox := max((maxW-boxW)/2, 0)
	oy := min(max((maxH-boxH)/3, 1), maxH-boxH)
	if p.cfg.Style == StyleCommand {
		oy = max((maxH-boxH)/2, 0)
	}
	out.Children = []components.SubSurface{{
		Origin:  components.Point{X: ox, Y: oy},
		Surface: panel,
		Z:       10,
	}}
	return out
}

func (p *Picker) panelWidth(maxW int) int {
	boxW := p.Width
	if boxW <= 0 && p.cfg.Style == StyleCommand {
		boxW = min(maxW-2, 76)
		boxW = max(boxW, min(maxW, 56))
	}
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
	if maxH < 7 {
		return max(maxH-3, 0), maxH
	}
	rows := max(len(p.filtered), 1) // one empty-state row
	boxH = min(min(rows, p.maxItems())+6, max(maxH-2, 7))
	return boxH - 6, boxH
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
	if panel.Size.Height < 4 || boxW < 6 {
		return
	}
	y := 2
	if panel.Size.Height < 7 {
		y = 1
	}
	if p.confirming {
		// The filter is frozen while a delete is pending; its line becomes the
		// destructive question so no extra row is needed in the box layout.
		msg := components.TruncateToWidth(p.cfg.ConfirmMessage, max(boxW-3, 1), ctx.Method)
		panel.Print(1, y, msg, th.Destructive, ctx.Method)
		return
	}
	panel.Print(1, y, chrome.FilterPrompt, th.Foreground, ctx.Method)
	avail := max(boxW-5, 1)
	q := p.Query()
	placeholder := false

	if q == "" {
		q = p.cfg.FilterHint
		placeholder = true
	}
	st := th.Foreground
	if placeholder {
		st = th.Muted
	}
	panel.Print(3, y, components.TruncateToWidth(q, avail, ctx.Method), st, ctx.Method)
	if !placeholder {
		before, _ := p.line.Split()
		curCol := xui.StringWidth(before, ctx.Method)
		curCol = max(min(curCol, avail-1), 0)

		panel.Cursor = &components.Point{X: 3 + curCol, Y: y}
	} else {
		panel.Cursor = &components.Point{X: 3, Y: y}
	}
}

type rowColumns struct {
	primaryX, primaryWidth, detailWidth int
}

// Measure the whole list, not the filtered page, so typing and scrolling keep
// both columns in place. A page with no descriptions is a single centered
// column; two-column pages keep fixed left edges instead.
func (p *Picker) columns(boxW int, method xui.WidthMethod) rowColumns {
	primaryW, detailW := 0, 0
	for _, item := range p.Items {
		primaryW = max(primaryW, xui.StringWidth(strings.TrimSpace(item.Primary), method))
		detailW = max(detailW, xui.StringWidth(strings.TrimSpace(item.Detail), method))
	}
	if p.cfg.PrimaryWidth > 0 {
		primaryW = min(primaryW, p.cfg.PrimaryWidth)
	}
	if detailW == 0 {
		primaryW = min(primaryW, max(boxW-2*(2+markerWidth), 0))
		primaryX := max((boxW-primaryW)/2, 2+markerWidth)
		if p.cfg.Style == StyleCommand {
			primaryX = 4
		}
		return rowColumns{primaryX: primaryX, primaryWidth: primaryW}
	}
	budget := max(boxW-2*(2+markerWidth)-columnGap, 0)
	primaryW = min(primaryW, budget-min(detailW, 8, budget/2))
	detailW = min(detailW, budget-primaryW)
	return rowColumns{primaryX: 2 + markerWidth, primaryWidth: primaryW, detailWidth: detailW}
}

func (p *Picker) drawRows(
	panel *components.Surface,
	ctx components.DrawContext,
	th components.Theme,
	boxW, visible int,
) {
	if visible == 0 {
		return
	}
	listY := 4
	if panel.Size.Height < 7 {
		listY = 2
	}
	if len(p.filtered) == 0 {
		msg := p.cfg.Empty
		if q := strings.TrimSpace(p.Query()); q != "" {
			msg = strings.ReplaceAll(p.cfg.EmptyFilter, "%q", strconv.Quote(q))
		}
		panel.Print(2, listY, components.TruncateToWidth(msg, boxW-3, ctx.Method), th.Muted, ctx.Method)
		return
	}
	columns := p.columns(boxW, ctx.Method)
	for i := range visible {
		ri := i + p.scroll
		if ri < 0 || ri >= len(p.filtered) {
			break
		}
		p.drawRow(panel, ctx, th, boxW, listY+i, ri, columns)
	}
}

func (p *Picker) drawRow(
	panel *components.Surface,
	ctx components.DrawContext,
	th components.Theme,
	boxW, y, ri int,
	columns rowColumns,
) {
	item := p.Items[p.filtered[ri].index]
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

	if item.Current {
		// The current row keeps its hue on top of the selection bar: the
		// selected branch must still say it is where you are.
		base.Fg = th.Success.Fg
		muted.Fg = th.Success.Fg
	}
	primary := components.TruncateToWidthSuffix(strings.TrimSpace(item.Primary), columns.primaryWidth, "…", ctx.Method)
	detail := components.TruncateToWidthSuffix(strings.TrimSpace(item.Detail), columns.detailWidth, "…", ctx.Method)
	if item.Leading != "" {
		panel.Print(2, y, components.TruncateToWidth(item.Leading, 1, ctx.Method), muted, ctx.Method)
	}
	idSt := base
	idSt.Bold = true
	panel.Print(columns.primaryX, y, primary, idSt, ctx.Method)
	if columns.detailWidth > 0 {
		detailStyle := base
		if p.cfg.Style == StyleCommand && !sel {
			detailStyle = th.Muted
			detailStyle.Dim = false
		}
		panel.Print(columns.primaryX+columns.primaryWidth+columnGap, y, detail, detailStyle, ctx.Method)
	}
}

func (p *Picker) drawHint(
	panel *components.Surface,
	ctx components.DrawContext,
	th components.Theme,
	boxW, boxH int,
) {
	if boxH < 4 || boxW < 4 {
		return
	}
	hint, short := p.cfg.Hint, chrome.ListHintShort("select")
	if p.cfg.HintShort != "" {
		short = p.cfg.HintShort
	}
	if p.confirming {
		hint = p.cfg.ConfirmHint
		if p.cfg.ConfirmShort != "" {
			short = p.cfg.ConfirmShort
		}
	}
	if xui.StringWidth(hint, ctx.Method) > boxW-2 {
		hint = short
	}
	y := boxH - 1
	w := xui.StringWidth(hint, ctx.Method)
	x := max((boxW-w)/2, 1)
	panel.Print(x, y, components.TruncateToWidth(hint, boxW-2, ctx.Method), th.Muted, ctx.Method)
}
