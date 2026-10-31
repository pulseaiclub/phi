package palette

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/util"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/layout"
)

// PaletteCommand is one entry in the command palette.
type PaletteCommand struct {
	ID       string
	Noun     string // category column, e.g. "settings"; empty = flat select list
	Verb     string // action / label, e.g. "theme" or "Dark (builtin)"
	Shortcut string // e.g. "Ctrl+K"
	Keywords []string
	Disabled bool
	// Submenu opens a nested picker (e.g. Select Theme).
	Submenu      []PaletteCommand
	SubmenuTitle string // header for the nested list; default Verb
	// KeepOpen leaves the palette open after Run (when not using Submenu).
	KeepOpen bool
	Run      func()
}

// Label returns the fuzzy-searchable label (noun + verb + keywords).
func (c PaletteCommand) Label() string {
	parts := make([]string, 0, 2+len(c.Keywords))
	if c.Noun != "" {
		parts = append(parts, strings.ToLower(c.Noun))
	}
	parts = append(parts, strings.ToLower(c.Verb))
	for _, k := range c.Keywords {
		parts = append(parts, strings.ToLower(k))
	}
	return strings.Join(parts, " ")
}

// CommandPalette is a Ctrl+K fuzzy picker overlay.
// Nested lists (Select Theme, etc.) are a stack of pages via Push/Pop or PaletteCommand.Submenu.
type CommandPalette struct {
	Open     bool
	Title    string
	Query    string
	Cursor   int // byte offset into Query
	Commands []PaletteCommand
	Selected int
	Theme    components.Theme
	MaxItems int // visible rows; default 12
	Width    int // panel width; 0 = auto
	OnClose  func()
	OnAccept func(PaletteCommand)
	// FocusReturn is focused when the palette closes (typically the tui input).
	FocusReturn components.Widget

	filtered []int // indices into Commands
	stack    []paletteFrame
}

// paletteFrame is one nested picker page under the current view.
type paletteFrame struct {
	Title    string
	Commands []PaletteCommand
	Query    string
	Cursor   int
	Selected int
}

func (p *CommandPalette) theme() components.Theme {
	if p.Theme.Border.Fg.Kind == 0 && p.Theme.Foreground.Fg.Kind == 0 {
		return components.DefaultTheme()
	}
	return p.Theme
}

func (p *CommandPalette) title() string {
	if p.Title == "" {
		return "Command Palette"
	}
	return p.Title
}

func (p *CommandPalette) maxItems() int {
	if p.MaxItems < 1 {
		return 12
	}
	return p.MaxItems
}

// Show opens the palette and resets query/selection.
func (p *CommandPalette) Show() {
	p.Open = true
	p.Query = ""
	p.Cursor = 0
	p.Selected = 0
	p.stack = nil
	if p.Title == "" {
		p.Title = "Command Palette"
	}
	p.refilter()
}

// Hide closes the palette and restores the root page.
func (p *CommandPalette) Hide() {
	for len(p.stack) > 0 {
		p.Pop()
	}
	p.Open = false
	p.Query = ""
	p.Cursor = 0
	if p.OnClose != nil {
		p.OnClose()
	}
}

// Push replaces the current list with a nested picker (e.g. Select Theme).
// Escape pops back to the previous page.
func (p *CommandPalette) Push(title string, cmds []PaletteCommand) {
	p.stack = append(p.stack, paletteFrame{
		Title:    p.Title,
		Commands: p.Commands,
		Query:    p.Query,
		Cursor:   p.Cursor,
		Selected: p.Selected,
	})
	p.Title = title
	p.Commands = cmds
	p.Query = ""
	p.Cursor = 0
	p.Selected = 0
	p.refilter()
}

// Pop restores the previous picker page. Returns false if already at the root.
func (p *CommandPalette) Pop() bool {
	n := len(p.stack)
	if n == 0 {
		return false
	}
	frame := p.stack[n-1]
	p.stack = p.stack[:n-1]
	p.Title = frame.Title
	p.Commands = frame.Commands
	p.Query = frame.Query
	p.Cursor = frame.Cursor
	p.Selected = frame.Selected
	p.refilter()
	return true
}

func (p *CommandPalette) returnFocus(ctx *components.EventContext) {
	if p.FocusReturn != nil {
		ctx.RequestFocus(p.FocusReturn)
	}
}

func (p *CommandPalette) refilter() {
	p.filtered = p.filtered[:0]
	q := strings.TrimSpace(p.Query)
	type scored struct {
		idx   int
		score float64
	}
	var ranked []scored
	for i, cmd := range p.Commands {
		ok, score := fuzzyMatch(q, cmd.Label())
		if !ok {
			continue
		}
		ranked = append(ranked, scored{i, score})
	}
	// Stable-ish: higher score first; empty query keeps original order.
	for i := 0; i < len(ranked); i++ {
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].score > ranked[i].score {
				ranked[i], ranked[j] = ranked[j], ranked[i]
			}
		}
	}
	for _, r := range ranked {
		p.filtered = append(p.filtered, r.idx)
	}
	if p.Selected >= len(p.filtered) {
		p.Selected = len(p.filtered) - 1
	}
	if p.Selected < 0 {
		p.Selected = 0
	}
}

func (p *CommandPalette) accept() (stillOpen bool) {
	if p.Selected < 0 || p.Selected >= len(p.filtered) {
		return p.Open
	}
	cmd := p.Commands[p.filtered[p.Selected]]
	if cmd.Disabled {
		return true
	}
	depth := len(p.stack)
	if len(cmd.Submenu) > 0 {
		title := cmd.SubmenuTitle
		if title == "" {
			title = cmd.Verb
		}
		p.Push(title, cmd.Submenu)
		return true
	}
	if p.OnAccept != nil {
		p.OnAccept(cmd)
	} else if cmd.Run != nil {
		cmd.Run()
	}
	// Run may have called Push for a custom nested flow.
	if len(p.stack) > depth || cmd.KeepOpen {
		return true
	}
	p.Hide()
	return false
}

// Handle drives palette interaction: query editing, selection navigation,
// accept on Enter, and close on Escape / Ctrl+K / click-outside.
func (p *CommandPalette) Handle(ctx *components.EventContext, ev xui.Event) {
	if !p.Open {
		return
	}
	switch e := ev.(type) {
	case xui.KeyEvent:
		if !e.Press {
			return
		}
		// Ctrl+K toggles closed (same binding that opens it).
		if e.Mods.Has(xui.ModCtrl) && e.Code == xui.KeyRune && (e.Rune == 'k' || e.Rune == 'K') {
			p.Hide()
			p.returnFocus(ctx)
			ctx.ConsumeAndRedraw()
			return
		}
		switch e.Code {
		case xui.KeyEscape:
			if p.Pop() {
				ctx.ConsumeAndRedraw()
				return
			}
			p.Hide()
			p.returnFocus(ctx)
			ctx.ConsumeAndRedraw()
			return
		case xui.KeyEnter:
			if !p.accept() {
				p.returnFocus(ctx)
			}
			ctx.ConsumeAndRedraw()
			return
		case xui.KeyUp:
			if p.Selected > 0 {
				p.Selected--
			}
			ctx.ConsumeAndRedraw()
			return
		case xui.KeyDown:
			if p.Selected < len(p.filtered)-1 {
				p.Selected++
			}
			ctx.ConsumeAndRedraw()
			return
		case xui.KeyTab:
			if e.Mods.Has(xui.ModShift) {
				if p.Selected > 0 {
					p.Selected--
				}
			} else if p.Selected < len(p.filtered)-1 {
				p.Selected++
			}
			ctx.ConsumeAndRedraw()
			return
		case xui.KeyBackspace:
			if p.Cursor > 0 {
				_, size := utf8.DecodeLastRuneInString(p.Query[:p.Cursor])
				p.Query = p.Query[:p.Cursor-size] + p.Query[p.Cursor:]
				p.Cursor -= size
				p.Selected = 0
				p.refilter()
			}
			ctx.ConsumeAndRedraw()
			return
		case xui.KeyLeft:
			if p.Cursor > 0 {
				_, size := utf8.DecodeLastRuneInString(p.Query[:p.Cursor])
				p.Cursor -= size
			}
			ctx.ConsumeAndRedraw()
			return
		case xui.KeyRight:
			if p.Cursor < len(p.Query) {
				_, size := utf8.DecodeRuneInString(p.Query[p.Cursor:])
				p.Cursor += size
			}
			ctx.ConsumeAndRedraw()
			return
		case xui.KeyHome:
			p.Cursor = 0
			ctx.ConsumeAndRedraw()
			return
		case xui.KeyEnd:
			p.Cursor = len(p.Query)
			ctx.ConsumeAndRedraw()
			return
		case xui.KeyRune:
			if e.Mods.Has(xui.ModCtrl) {
				switch e.Rune {
				case 'n', 'N':
					if p.Selected < len(p.filtered)-1 {
						p.Selected++
					}
					ctx.ConsumeAndRedraw()
					return
				case 'p', 'P':
					if p.Selected > 0 {
						p.Selected--
					}
					ctx.ConsumeAndRedraw()
					return
				}
				// Let other Ctrl chords bubble (e.g. future bindings).
				return
			}
			if e.Mods.Has(xui.ModAlt) {
				return
			}
			if e.Rune >= 0x20 || e.Rune == '\t' {
				r := string(e.Rune)
				p.Query = p.Query[:p.Cursor] + r + p.Query[p.Cursor:]
				p.Cursor += len(r)
				p.Selected = 0
				p.refilter()
				ctx.ConsumeAndRedraw()
			}
			return
		}
		ctx.Consume = true
	case xui.PasteEvent:
		text := util.ReplaceAll(e.Text, "\n", " ")
		text = util.ReplaceAll(text, "\r", " ")
		p.Query = p.Query[:p.Cursor] + text + p.Query[p.Cursor:]
		p.Cursor += len(text)
		p.Selected = 0
		p.refilter()
		ctx.ConsumeAndRedraw()
	case xui.MouseEvent:
		if e.Action == xui.MousePress && e.Button == xui.MouseLeft {
			ctx.ConsumeAndRedraw()
		}
	}
}

// Draw renders the overlay dim layer and the bordered palette panel with
// title, query prompt, and the scrolled filtered command list.
func (p *CommandPalette) Draw(ctx components.DrawContext) components.Surface {
	th := p.theme()
	maxW, maxH := ctx.Max.Width, ctx.Max.Height
	if maxW <= 0 {
		maxW = 80
	}
	if maxH <= 0 {
		maxH = 24
	}
	if !p.Open {
		return components.Surface{Size: components.Size{Width: maxW, Height: maxH}, Widget: p}
	}
	if p.filtered == nil {
		p.refilter()
	}

	boxW := p.Width
	if boxW <= 0 {
		boxW = maxW * 3 / 4
		boxW = min(boxW, 72)
		if boxW < 40 {
			boxW = maxW
			boxW = min(boxW, 60)
		}
	}
	if boxW > maxW-2 {
		boxW = maxW - 2
		if boxW < 20 {
			boxW = maxW
		}
	}

	innerW := boxW - 2
	catW := categoryWidth(p.Commands, ctx.Method)
	maxVisible := p.maxItems()
	availRows := maxH - 6 // borders + title gap + prompt + margin
	availRows = max(availRows, 3)
	if maxVisible > availRows {
		maxVisible = availRows
	}
	nItems := len(p.filtered)
	visible := nItems
	visible = min(visible, maxVisible)
	visible = max(visible, 1) // empty state row

	// Layout: border + prompt row + item rows
	boxH := 2 + 1 + visible
	if boxH > maxH-2 {
		boxH = maxH - 2
		visible = boxH - 3
		if visible < 1 {
			visible = 1
			boxH = 4
		}
	}

	// Scroll window so selection stays visible.
	scroll := 0
	if p.Selected >= visible {
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
	// Opaque panel background so tui doesn't bleed through.
	fillStyle := xui.Style{Fg: th.Foreground.Fg}
	for y := 0; y < boxH; y++ {
		for x := 0; x < boxW; x++ {
			panel.SetCell(x, y, xui.Cell{Char: " ", Width: 1, Style: fillStyle})
		}
	}
	layout.DrawRoundedBorder(&panel, layout.BorderRounded, th.Border, nil, nil, nil, nil, ctx.Method)

	// Centered title on top border.
	title := " " + p.title() + " "
	tw := xui.StringWidth(title, ctx.Method)
	tx := (boxW - tw) / 2
	tx = max(tx, 1)
	titleSt := chrome.PanelTitle(th)
	panel.Print(tx, 0, title, titleSt, ctx.Method)

	// Prompt: "> query"
	promptY := 1
	panel.Print(1, promptY, chrome.FilterPrompt, th.Foreground, ctx.Method)
	q := p.Query
	qx := 3
	availQ := innerW - 2
	availQ = max(availQ, 1)
	panel.Print(qx, promptY, components.TruncateToWidth(q, availQ, ctx.Method), th.Foreground, ctx.Method)
	curCol := xui.StringWidth(q[:min(p.Cursor, len(q))], ctx.Method)
	if curCol >= availQ {
		curCol = availQ - 1
		curCol = max(curCol, 0)
	}
	panel.Cursor = &components.Point{X: qx + curCol, Y: promptY}

	padL := 1
	listY := 2
	if nItems == 0 {
		panel.Print(padL+1, listY, "No matching commands", th.Muted, ctx.Method)
	} else {
		for row := 0; row < visible; row++ {
			fi := row + scroll
			if fi < 0 || fi >= nItems {
				break
			}
			cmd := p.Commands[p.filtered[fi]]
			sel := fi == p.Selected
			y := listY + row
			rowStyle := th.Foreground
			nounStyle := th.Muted
			shortcutStyle := th.ToolName
			if sel {
				bg := th.SelectionBg.Bg
				for x := 1; x < boxW-1; x++ {
					panel.SetCell(x, y, xui.Cell{Char: " ", Width: 1, Style: xui.Style{Bg: bg}})
				}
				// Text must carry Bg too — Print replaces the whole cell style.
				fg := th.SelectionFg
				fg.Bg = bg
				rowStyle = fg
				nounStyle = fg
				shortcutStyle = fg
				shortcutStyle.Bold = true
			}
			if cmd.Disabled {
				rowStyle.Dim = true
				nounStyle.Dim = true
			}

			x := padL
			noun := strings.ToLower(cmd.Noun)
			verb := strings.ToLower(cmd.Verb)
			if catW > 0 {
				nw := xui.StringWidth(noun, ctx.Method)
				nx := x + catW - nw
				nx = max(nx, x)
				panel.Print(nx, y, components.TruncateToWidth(noun, catW, ctx.Method), nounStyle, ctx.Method)
				x += catW + 2
			}
			verbSt := rowStyle
			verbSt.Bold = true
			if cmd.Disabled {
				verbSt.Dim = true
			}
			shortcutW := 0
			if cmd.Shortcut != "" {
				shortcutW = xui.StringWidth(cmd.Shortcut, ctx.Method) + 1
			}
			verbAvail := boxW - 1 - x - shortcutW
			verbAvail = max(verbAvail, 1)
			panel.Print(x, y, components.TruncateToWidth(verb, verbAvail, ctx.Method), verbSt, ctx.Method)
			if cmd.Shortcut != "" {
				sw := xui.StringWidth(cmd.Shortcut, ctx.Method)
				panel.Print(boxW-1-sw, y, cmd.Shortcut, shortcutStyle, ctx.Method)
			}
		}
	}

	out := components.Surface{Size: components.Size{Width: maxW, Height: maxH}, Widget: p}
	ox := (maxW - boxW) / 2
	oy := (maxH - boxH) / 3
	if ox < 0 {
		ox = 0
	}
	if oy < 1 {
		oy = 1
	}
	out.Children = []components.SubSurface{{
		Origin:  components.Point{X: ox, Y: oy},
		Surface: panel,
		Z:       10,
	}}
	return out
}

func categoryWidth(cmds []PaletteCommand, method xui.WidthMethod) int {
	maxVal := 0
	for _, c := range cmds {
		w := xui.StringWidth(strings.ToLower(c.Noun), method)
		if w > maxVal {
			maxVal = w
		}
	}
	return maxVal
}

// fuzzyMatch: empty query matches all;
// otherwise require a subsequence / substring score above ~0.15.
func fuzzyMatch(query, label string) (bool, float64) {
	q := strings.ToLower(strings.TrimSpace(query))
	l := strings.ToLower(label)
	if q == "" {
		return true, 1
	}
	if l == q {
		return true, 1
	}
	if strings.HasPrefix(l, q) {
		return true, 0.95
	}
	if strings.Contains(l, q) {
		return true, 0.75
	}
	score := subsequenceScore(l, q)
	return score > 0.15, score
}

func subsequenceScore(label, query string) float64 {
	if query == "" {
		return 1
	}
	li, matched, consecutive, gaps := 0, 0, 0, 0
	run := 0
	for _, qr := range query {
		found := false
		for li < len(label) {
			lr, size := utf8.DecodeRuneInString(label[li:])
			li += size
			if unicode.ToLower(lr) == unicode.ToLower(qr) {
				matched++
				run++
				if run > 1 {
					consecutive++
				}
				found = true
				break
			}
			if matched > 0 {
				gaps++
			}
			run = 0
		}
		if !found {
			return 0
		}
	}
	base := float64(matched) / float64(utf8.RuneCountInString(query))
	bonus := float64(consecutive) * 0.05
	penalty := float64(gaps) * 0.02
	score := base + bonus - penalty
	if score > 1 {
		score = 1
	}
	if score < 0 {
		score = 0
	}
	return score
}
