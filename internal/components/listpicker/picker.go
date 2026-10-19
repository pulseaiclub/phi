// Package listpicker provides a centered, opaque, filterable list overlay.
// Domain packages map their rows into Item; the picker stays UI-only. The one
// filter line serves both halves: plain text searches Items, `!name` searches
// the caller's Commands.
package listpicker

import (
	"slices"
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

// CommandPrefix opens the command line: a filter query that starts with it is
// matched against Commands instead of Items.
const CommandPrefix = "!"

// Command is one `!name` action reachable from the filter line. Domains keep
// their own vocabulary; the picker only offers, filters, and dispatches these.
// Commands are standalone by design — an operand travels on the line itself
// (`!checkout feature/x`), so they never need the highlighted row.
type Command struct {
	Name     string // typed after CommandPrefix ("fetch")
	Detail   string // one-line description beside the name
	Keywords string // extra filter haystack (aliases)
	// Run executes with the operand typed after the name, trimmed: `!fetch
	// origin` runs with "origin". The picker is still open here, so Run may
	// SetItems or Hide. Nil swaps the row list without dispatching anything.
	Run func(args string)
	// KeepOpen clears the line and returns to the rows instead of closing once
	// Run returns — for commands that refresh the list and stay put.
	KeepOpen bool
}

// ShowConfig customizes chrome copy for one opening.
type ShowConfig struct {
	Title          string // default "Select"
	FilterHint     string // placeholder when query empty
	Empty          string // no items at all
	EmptyFilter    string // no matches; may contain %q for the query
	Hint           string // bottom border caption
	ConfirmMessage string // replaces the filter prompt while confirming a delete
	ConfirmHint    string // bottom border caption while confirming; default " y/enter delete · n/esc cancel "
	CommandTitle   string // title while the filter line holds a command; default "Commands"
	CommandHint    string // bottom caption in command mode; default chrome.ListHintExtra("run")
	EmptyCommand   string // no command matches; may contain %q for the typed name
	LeadingWidth   int    // fixed leading column; 0 = auto from content / 10
	PrimaryWidth   int    // fixed primary column; 0 = 8
}

// Picker is a filterable list overlay. Enter accepts the selection.
// Ctrl+X arms deletion of the selected row; Y/Enter fires OnDelete, N/Esc
// disarms. The overlay stays open after a delete so callers can remove
// several rows without reopening.
//
// With Commands set, a Query that opens with CommandPrefix searches those
// commands instead of Items, and Enter runs the highlighted one. `!` stays an
// ordinary filter rune while Commands is empty, so opting in is the caller's
// call.
type Picker struct {
	Open     bool
	Query    string
	Cursor   int
	Items    []Item
	Selected int
	Theme    components.Theme
	MaxItems int
	Width    int
	// Commands are the `!name` actions offered on the filter line. Set them
	// before the user types; the rows are rebuilt on every filter change.
	Commands []Command

	OnAccept    func(Item)
	OnDelete    func(Item)
	OnClose     func()
	FocusReturn components.Widget

	cfg         ShowConfig
	filtered    []hit
	cmdItems    []Item
	scroll      int
	confirming  bool
	commandMode bool // Query opens with CommandPrefix and Commands is non-empty
	hintSet     bool // caller supplied ShowConfig.Hint; keep the command advert out
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
	p.hintSet = cfg.Hint != ""
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
		cfg.ConfirmHint = " y/enter delete" + chrome.Sep + "n/esc cancel "
	}
	if cfg.CommandTitle == "" {
		cfg.CommandTitle = "Commands"
	}
	if cfg.CommandHint == "" {
		cfg.CommandHint = chrome.ListHintExtra("run")
	}
	if cfg.EmptyCommand == "" {
		cfg.EmptyCommand = "No command matches %q"
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
	p.commandMode = false
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
	tierText                      // only Detail / Keywords / Badge matched
	tierContains                  // Primary or ID contains the query
	tierPrefix                    // Primary or ID starts with it
	tierExact                     // Primary or ID is exactly the query
)

func matchTierOf(item Item, q string) matchTier {
	primary, id := strings.ToLower(item.Primary), strings.ToLower(item.ID)
	switch {
	case primary == q || id == q:
		return tierExact
	case strings.HasPrefix(primary, q) || strings.HasPrefix(id, q):
		return tierPrefix
	case strings.Contains(primary, q) || strings.Contains(id, q):
		return tierContains
	case itemMatches(item, q):
		return tierText
	}
	return tierNone
}

func (p *Picker) refilter() {
	name, _, commands := p.commandQuery()
	p.commandMode = commands
	if commands {
		p.cmdItems = p.commandRows()
	}
	q := strings.ToLower(strings.TrimSpace(p.Query))
	if commands {
		// Anything after the name is an operand, not a filter: `!fetch origin`
		// must keep offering fetch.
		q = strings.ToLower(name)
	}
	p.filtered = p.filtered[:0]
	for i, item := range p.rows() {
		tier := tierText // no query: every row stays, in the caller's order
		if q != "" {
			tier = matchTierOf(item, q)
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
		item.ID, item.Leading, item.Primary, item.Detail, item.Badge, item.Keywords,
	}, " "))
	return strings.Contains(hay, q)
}

// commandWidth sizes the primary column for the offered commands: the name is
// what the user types and what Run receives, so cutting it at the item width
// would misreport what Enter does.
func (p *Picker) commandWidth(method xui.WidthMethod) int {
	w := 0
	for _, c := range p.Commands {
		w = max(w, xui.StringWidth(CommandPrefix+strings.TrimSpace(c.Name), method))
	}
	return w
}

// rows is what the filter line is searching right now: the caller's Items, or
// the command rows while the line holds CommandPrefix.
func (p *Picker) rows() []Item {
	if p.commandMode {
		return p.cmdItems
	}
	return p.Items
}

// commandRows maps Commands onto rows so commands reuse the picker's filter,
// ranking, selection, and drawing untouched. Rebuilt per filter change: a
// handful of rows, and a cache would only add invalidation to get wrong.
func (p *Picker) commandRows() []Item {
	if len(p.Commands) == 0 {
		return nil
	}
	rows := make([]Item, 0, len(p.Commands))
	for _, c := range p.Commands {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			continue
		}
		rows = append(rows, Item{
			ID:       name,
			Primary:  CommandPrefix + name,
			Detail:   c.Detail,
			Keywords: name + " " + c.Keywords,
		})
	}
	return rows
}

// commandQuery splits the line into the command name to match and the operand
// after it. ok is false for ordinary filtering: no Commands configured, or a
// line that does not open with CommandPrefix.
func (p *Picker) commandQuery() (name, args string, ok bool) {
	if len(p.Commands) == 0 || !strings.HasPrefix(p.Query, CommandPrefix) {
		return "", "", false
	}
	name, args, _ = strings.Cut(p.Query[len(CommandPrefix):], " ")
	return name, strings.TrimSpace(args), true
}

func (p *Picker) commandByName(name string) (Command, bool) {
	for _, c := range p.Commands {
		if strings.EqualFold(strings.TrimSpace(c.Name), name) {
			return c, true
		}
	}
	return Command{}, false
}

// clearQuery empties the filter line and hands the rows back to the caller.
func (p *Picker) clearQuery() {
	p.Query = ""
	p.Cursor = 0
	p.Selected = 0
	p.scroll = 0
	p.refilter()
}

func (p *Picker) selectPreferred() {
	if strings.TrimSpace(p.Query) != "" {
		return
	}
	rows := p.rows()
	for i, fi := range p.filtered {
		if rows[fi.index].Badge != "" {
			p.Selected = i
			return
		}
	}
}

func (p *Picker) selectedItem() (Item, bool) {
	if p.Selected < 0 || p.Selected >= len(p.filtered) {
		return Item{}, false
	}
	rows := p.rows()
	idx := p.filtered[p.Selected].index
	if idx < 0 || idx >= len(rows) {
		return Item{}, false
	}
	return rows[idx], true
}

func (p *Picker) accept() {
	item, ok := p.selectedItem()
	if !ok {
		return
	}
	if p.commandMode {
		if cmd, found := p.commandByName(item.ID); found {
			p.runCommand(cmd)
		}
		return
	}
	if p.OnAccept != nil {
		p.OnAccept(item)
	}
	p.Hide()
}

// runCommand dispatches the highlighted command with the operand typed after
// its name. Run still sees an open picker so it can refresh the rows; unless it
// closes the picker or asks to keep it, the overlay closes like an accept.
func (p *Picker) runCommand(cmd Command) {
	_, args, _ := p.commandQuery()
	if cmd.Run != nil {
		cmd.Run(args)
	}
	if !p.Open {
		return
	}
	if cmd.KeepOpen {
		p.clearQuery()
		return
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
	case xui.KeyEnter, xui.KeyTab:
		// Tab picks the highlighted row, like Enter. There is no composer text to
		// complete here, and Up / Down / Ctrl+P / Ctrl+N already navigate.
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
				// picker domains without one keep Ctrl+X inert. Never while the
				// line holds a command: the selected row is not theirs.
				if p.OnDelete != nil && !p.commandMode {
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

	label, total := p.cfg.Title, len(p.rows())
	if p.commandMode {
		label = p.cfg.CommandTitle
	}
	count := strconv.Itoa(total)
	if n := len(p.filtered); n != total {
		count = strconv.Itoa(n) + "/" + strconv.Itoa(total)
	}
	title := " " + label + " · " + count + " "
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
			pattern := p.cfg.EmptyFilter
			if p.commandMode {
				pattern = p.cfg.EmptyCommand
			}
			msg = strings.ReplaceAll(pattern, "%q", strconv.Quote(q))
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
	item := p.rows()[p.filtered[ri].index]
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

	leadW, primW := p.cfg.LeadingWidth, p.cfg.PrimaryWidth
	if p.commandMode {
		// Commands carry no leading column; skipping its width keeps every name
		// lined up with the prompt instead of under a blank gutter. The primary
		// column widens to the names (see commandWidth) so none is cut short.
		leadW = 0
		primW = p.commandWidth(ctx.Method)
	}

	x := 2
	if item.Leading != "" {
		panel.Print(x, y, layout.TruncateToWidth(item.Leading, leadW, ctx.Method), muted, ctx.Method)
	}
	x += leadW

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
	hint, short := p.cfg.Hint, chrome.ListHintShort("select")
	switch {
	case p.confirming:
		hint = p.cfg.ConfirmHint
	case p.commandMode:
		hint, short = p.cfg.CommandHint, chrome.ListHintShort("run")
	case !p.hintSet && len(p.Commands) > 0:
		// Advertise the line itself, or nobody finds the prefix. A caller that
		// supplied Hint has already said what belongs down here.
		hint = chrome.ListHintExtra("select", CommandPrefix+" commands")
	}
	if xui.StringWidth(hint, ctx.Method) > boxW-2 {
		hint = short
	}
	y := boxH - 1
	w := xui.StringWidth(hint, ctx.Method)
	x := max((boxW-w)/2, 1)
	panel.Print(x, y, layout.TruncateToWidth(hint, boxW-2, ctx.Method), th.Muted, ctx.Method)
}
