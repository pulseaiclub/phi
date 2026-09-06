// Package sessiontree renders the inline session tree. Callers own persistence and navigation.
package sessiontree

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/layout"
	"github.com/pulseaiclub/phi/internal/components/tree"
)

var Filters = []string{"default", "no-tools", "user-only", "labeled-only", "all"}

type Item struct {
	TextlessAssistant               bool
	ID, ParentID, Kind, Text, Label string
	Time, LabelTime                 time.Time
}

type Row struct {
	Item        Item
	ParentID    string
	Prefix      string
	Depth       int
	Active      bool
	HasChildren bool
}

type Picker struct {
	Open                     bool
	Theme                    components.Theme
	Query                    string
	Filter                   string
	Selected                 int
	Mode                     string // browse | summary | prompt | label | wait
	Input                    string
	Choice                   int
	Status                   string
	Keys                     map[string]string
	OnAccept                 func(Item)
	OnChoice                 func(bool, string)
	OnLabel                  func(Item, string) error
	OnCopy                   func(string)
	OnClose                  func()
	OnCancel                 func()
	items                    []Item
	current                  string
	rows                     []Row
	collapsed                map[string]bool
	scroll, horizontal, page int
}

func (p *Picker) Show(items []Item, current, filter string, keys map[string]string) {
	p.Open = true
	p.Mode, p.Query, p.Input, p.Status = "browse", "", "", ""
	p.Filter = filter
	if !slices.Contains(Filters, filter) {
		p.Filter = "default"
	}
	p.Keys = keys
	p.collapsed = make(map[string]bool)
	p.scroll, p.horizontal = 0, 0
	p.items, p.current = slices.Clone(items), current
	p.rebuild(current)
}

func (p *Picker) Refresh(items []Item, current string) {
	id := p.selected().ID
	p.items, p.current = slices.Clone(items), current
	p.rebuild(id)
}

func (p *Picker) Close() {
	p.Open = false
	if p.OnClose != nil {
		p.OnClose()
	}
}

func (p *Picker) Rows() []Row { return slices.Clone(p.rows) }

func (p *Picker) selected() Item {
	if p.Selected < 0 || p.Selected >= len(p.rows) {
		return Item{}
	}
	return p.rows[p.Selected].Item
}

func (p *Picker) rebuild(preferred string) {
	byID := make(map[string]Item, len(p.items))
	for _, item := range p.items {
		byID[item.ID] = item
	}
	active := make(map[string]bool)
	for id := p.current; id != "" && !active[id]; id = byID[id].ParentID {
		active[id] = true
	}
	visible := make(map[string]bool)
	for _, item := range p.items {
		visible[item.ID] = p.matches(item)
	}
	children := make(map[string][]Item)
	parents := make(map[string]string)
	for _, item := range p.items {
		if !visible[item.ID] {
			continue
		}
		parent := item.ParentID
		seen := map[string]bool{item.ID: true}
		for parent != "" && !visible[parent] && !seen[parent] {
			seen[parent] = true
			parent = byID[parent].ParentID
		}
		if seen[parent] {
			parent = ""
		}
		parents[item.ID] = parent
		children[parent] = append(children[parent], item)
	}
	for key := range children {
		slices.SortStableFunc(children[key], func(a, b Item) int {
			if active[a.ID] == active[b.ID] {
				return 0
			}
			if active[a.ID] {
				return -1
			}
			return 1
		})
	}
	var build func(string, map[string]bool) []tree.Node[Item]
	build = func(parent string, seen map[string]bool) []tree.Node[Item] {
		var nodes []tree.Node[Item]
		for _, item := range children[parent] {
			if seen[item.ID] {
				continue
			}
			seen[item.ID] = true
			n := tree.Node[Item]{Item: item}
			if !p.collapsed[item.ID] || strings.TrimSpace(p.Query) != "" {
				n.Children = build(item.ID, seen)
			}
			nodes = append(nodes, n)
		}
		return nodes
	}
	p.rows = []Row{{Item: Item{Kind: "root", Text: "Start of session"}, Active: p.current == ""}}
	for _, flat := range tree.Flatten(build("", make(map[string]bool))) {
		p.rows = append(
			p.rows,
			Row{
				Item:        flat.Item,
				ParentID:    parents[flat.Item.ID],
				Prefix:      tree.Prefix(flat, tree.Style{Indent: 2}),
				Depth:       flat.Depth,
				Active:      active[flat.Item.ID],
				HasChildren: len(children[flat.Item.ID]) > 0,
			},
		)
	}
	seenPreferred := make(map[string]bool)
	for {
		for i, row := range p.rows {
			if row.Item.ID == preferred {
				p.Selected = i
				return
			}
		}
		if preferred == "" || seenPreferred[preferred] {
			p.Selected = 0
			return
		}
		seenPreferred[preferred] = true
		preferred = byID[preferred].ParentID
	}
}

func (p *Picker) matches(item Item) bool {
	// Textless intermediate assistant entries are hidden even in "all", as in Pi.
	if item.Kind == "assistant" && (item.TextlessAssistant || strings.TrimSpace(item.Text) == "") &&
		item.ID != p.current {
		return false
	}
	switch p.Filter {
	case "default":
		if item.Kind == "tool" || item.Kind == "model" {
			return false
		}
	case "no-tools":
		if item.Kind == "tool" || item.Kind == "bash" {
			return false
		}
	case "user-only":
		if item.Kind != "user" {
			return false
		}
	case "labeled-only":
		if item.Label == "" {
			return false
		}
	}
	hay := strings.ToLower(item.Text + " " + item.Kind + " " + item.Label + " " + item.ID)
	for word := range strings.FieldsSeq(strings.ToLower(p.Query)) {
		if !strings.Contains(hay, word) {
			return false
		}
	}
	return true
}

func (p *Picker) Handle(ctx *components.EventContext, event xui.Event) {
	if !p.Open {
		return
	}
	switch ev := event.(type) {
	case xui.PasteEvent:
		p.insert(singleLine(ev.Text))
	case xui.KeyEvent:
		if !ev.Press {
			return
		}
		p.handleKey(ev)
	default:
		return
	}
	ctx.ConsumeAndRedraw()
}

func (p *Picker) handleKey(ev xui.KeyEvent) {
	name := keyName(ev)
	if name == "escape" {
		switch p.Mode {
		case "wait":
			if p.OnCancel != nil {
				p.OnCancel()
			}
			p.Status = "Canceling…"
		case "summary", "prompt", "label":
			p.Mode, p.Status = "browse", ""
		default:
			p.Close()
		}
		return
	}
	if p.Mode == "wait" {
		return
	}
	if p.Mode == "summary" {
		switch name {
		case "up":
			p.Choice = max(0, p.Choice-1)
		case "down", "tab":
			p.Choice = (p.Choice + 1) % 3
		case "enter":
			if p.Choice == 2 {
				p.Mode, p.Input = "prompt", ""
			} else if p.OnChoice != nil {
				p.OnChoice(p.Choice == 1, "")
			}
		}
		return
	}
	if p.Mode == "prompt" || p.Mode == "label" {
		if name == "enter" {
			if p.Mode == "prompt" && p.OnChoice != nil {
				p.OnChoice(true, p.Input)
			}
			if p.Mode == "label" && p.OnLabel != nil {
				if err := p.OnLabel(p.selected(), p.Input); err != nil {
					p.Status = err.Error()
				} else {
					p.Mode = "browse"
				}
			}
		} else if name == "backspace" {
			p.Input = backspace(p.Input)
		} else if ev.Code == xui.KeyRune && !ev.Mods.Has(xui.ModCtrl) && !ev.Mods.Has(xui.ModAlt) {
			p.Input += string(ev.Rune)
		}
		return
	}
	id := p.selected().ID
	action := p.action(name)
	switch action {
	case "accept":
		if len(p.rows) > 0 && p.OnAccept != nil {
			p.OnAccept(p.selected())
		}
	case "up":
		p.Selected = max(0, p.Selected-1)
	case "down":
		p.Selected = min(len(p.rows)-1, p.Selected+1)
	case "page_up":
		p.Selected = max(0, p.Selected-max(p.page, 1))
	case "page_down":
		p.Selected = min(len(p.rows)-1, p.Selected+max(p.page, 1))
	case "first":
		p.Selected = 0
	case "last":
		p.Selected = len(p.rows) - 1
	case "filter", "filter_back":
		i := slices.Index(Filters, p.Filter)
		if action == "filter_back" {
			i += len(Filters) - 2
		}
		p.Filter = Filters[(i+1)%len(Filters)]
		p.rebuild(id)
	case "collapse":
		if id != "" && p.rows[p.Selected].HasChildren && !p.collapsed[id] {
			p.collapsed[id] = true
			p.rebuild(id)
		} else {
			p.selectID(p.rows[p.Selected].ParentID)
		}
	case "expand":
		if p.collapsed[id] {
			delete(p.collapsed, id)
			p.rebuild(id)
		} else if p.Selected+1 < len(p.rows) && p.rows[p.Selected+1].ParentID == id {
			p.Selected++
		}
	case "branch_up":
		p.jumpBranch(-1)
	case "branch_down":
		p.jumpBranch(1)
	case "pan_left":
		p.horizontal = max(0, p.horizontal-8)
	case "pan_right":
		p.horizontal += 8
	case "label":
		if id != "" {
			p.Mode, p.Input, p.Status = "label", p.selected().Label, ""
		}
	case "copy":
		if p.OnCopy != nil {
			p.OnCopy(p.selected().Text)
		}
	default:
		if name == "backspace" {
			p.Query = backspace(p.Query)
			p.rebuild(id)
		} else if ev.Code == xui.KeyRune && !ev.Mods.Has(xui.ModCtrl) && !ev.Mods.Has(xui.ModAlt) {
			p.insert(string(ev.Rune))
		}
	}
	if p.selected().ID != id {
		p.horizontal = 0
	}
}

func (p *Picker) insert(s string) {
	if p.Mode == "prompt" || p.Mode == "label" {
		p.Input += s
		return
	}
	if p.Mode != "browse" {
		return
	}
	id := p.selected().ID
	p.Query += s
	p.rebuild(id)
}

func (p *Picker) selectID(id string) {
	for i, row := range p.rows {
		if row.Item.ID == id {
			p.Selected = i
			return
		}
	}
}

func (p *Picker) jumpBranch(direction int) {
	siblings := make(map[string]int)
	for _, row := range p.rows {
		if row.Item.ID != "" {
			siblings[row.ParentID]++
		}
	}
	for i := p.Selected + direction; i >= 0 && i < len(p.rows); i += direction {
		if siblings[p.rows[i].ParentID] > 1 || i == 0 {
			p.Selected = i
			return
		}
	}
	if direction > 0 {
		p.Selected = len(p.rows) - 1
	} else {
		p.Selected = 0
	}
}

var DefaultKeys = map[string]string{
	"accept":      "enter",
	"up":          "up",
	"down":        "down",
	"page_up":     "pageup",
	"page_down":   "pagedown",
	"first":       "home",
	"last":        "end",
	"filter":      "tab",
	"filter_back": "shift+tab",
	"collapse":    "left",
	"expand":      "right",
	"branch_up":   "ctrl+up",
	"branch_down": "ctrl+down",
	"pan_left":    "alt+left",
	"pan_right":   "alt+right",
	"label":       "ctrl+l",
	"copy":        "ctrl+y",
}

func (p *Picker) action(key string) string {
	for action, binding := range DefaultKeys {
		if configured := p.Keys[action]; configured != "" {
			binding = configured
		}
		if strings.EqualFold(key, binding) {
			return action
		}
	}
	return ""
}

func keyName(ev xui.KeyEvent) string {
	name := ""
	switch ev.Code {
	case xui.KeyRune:
		name = strings.ToLower(string(ev.Rune))
	case xui.KeyEscape:
		return "escape"
	case xui.KeyEnter:
		name = "enter"
	case xui.KeyTab:
		name = "tab"
	case xui.KeyUp:
		name = "up"
	case xui.KeyDown:
		name = "down"
	case xui.KeyLeft:
		name = "left"
	case xui.KeyRight:
		name = "right"
	case xui.KeyPageUp:
		name = "pageup"
	case xui.KeyPageDown:
		name = "pagedown"
	case xui.KeyHome:
		name = "home"
	case xui.KeyEnd:
		name = "end"
	case xui.KeyBackspace:
		name = "backspace"
	}
	if ev.Mods.Has(xui.ModShift) && ev.Code != xui.KeyRune {
		name = "shift+" + name
	}
	if ev.Mods.Has(xui.ModAlt) {
		name = "alt+" + name
	}
	if ev.Mods.Has(xui.ModCtrl) {
		name = "ctrl+" + name
	}
	return name
}

func backspace(s string) string {
	if s == "" {
		return s
	}
	_, size := utf8.DecodeLastRuneInString(s)
	return s[:len(s)-size]
}

func singleLine(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

func (p *Picker) help(width int, method xui.WidthMethod) []string {
	text := "↑↓ move  ←→ fold  PgUp/PgDn page  Ctrl+↑↓ branch  Tab filter  Ctrl+L label  Ctrl+Y copy  Alt+←→ pan  Enter select  Esc close"
	if len(p.Keys) > 0 {
		var custom strings.Builder
		for _, action := range []string{"up", "down", "collapse", "expand", "page_up", "page_down", "branch_up", "branch_down", "filter", "label", "copy", "pan_left", "pan_right", "accept"} {
			key := p.Keys[action]
			if key == "" {
				key = DefaultKeys[action]
			}
			custom.WriteString(key + " " + action + "  ")
		}
		custom.WriteString("Esc close")
		text = custom.String()
	}
	var lines []string
	line := ""
	for word := range strings.FieldsSeq(text) {
		if line != "" && xui.StringWidth(line+" "+word, method) > max(width, 1) {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += layout.TruncateToWidth(word, max(width, 1), method)
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func (p *Picker) PreferredHeight(width int, method xui.WidthMethod) int {
	return 3 + min(max(len(p.rows), 3), 12) + len(p.help(max(width-4, 1), method))
}

func (p *Picker) Draw(ctx components.DrawContext) components.Surface {
	w, h := max(ctx.Max.Width, 0), max(ctx.Max.Height, 0)
	surface := components.NewSurface(w, h, p)
	if !p.Open || w == 0 || h == 0 {
		return surface
	}
	th := p.Theme
	layout.DrawRoundedBorder(&surface, layout.BorderRounded, th.Border, nil, nil, nil, nil, ctx.Method)
	print := func(y int, text string, style xui.Style) {
		if y >= 0 && y < h {
			surface.Print(1, y, layout.TruncateToWidth(singleLine(text), max(w-2, 0), ctx.Method), style, ctx.Method)
		}
	}
	print(0, " /tree · "+p.Filter+" ", th.IdentityOrSuccess())
	if p.Mode == "wait" {
		print(1, "Stopping current execution / navigating… Esc cancels", th.Muted)
		print(2, p.Status, th.Warning)
		return surface
	}
	if p.Mode == "summary" {
		print(1, "Carry context from the branch you are leaving?", th.Foreground)
		for i, option := range []string{"No summary", "Summarize branch", "Summarize with custom instructions"} {
			prefix := "  "
			if i == p.Choice {
				prefix = "> "
			}
			print(i+2, prefix+option, th.Foreground)
		}
		print(h-1, " ↑↓ choose · Enter select · Esc back ", th.Muted)
		return surface
	}
	if p.Mode == "prompt" || p.Mode == "label" {
		title := "Summary instructions: "
		if p.Mode == "label" {
			title = "Label (empty clears): "
		}
		print(1, title+p.Input, th.Foreground)
		print(2, p.Status, th.Warning)
		print(h-1, " Enter confirm · Esc back ", th.Muted)
		return surface
	}
	help := p.help(max(w-4, 1), ctx.Method)
	helpRows := min(len(help), max(h-5, 0))
	p.page = max(0, h-3-helpRows)
	p.scroll = max(0, min(p.scroll, p.Selected))
	if p.page > 0 && p.Selected >= p.scroll+p.page {
		p.scroll = p.Selected - p.page + 1
	}
	print(1, "Search: "+p.Query, th.Foreground)
	for i := 0; i < p.page && i+p.scroll < len(p.rows); i++ {
		index := p.scroll + i
		row := p.rows[index]
		mark := "  "
		if row.Active {
			mark = "• "
		}
		if row.Item.ID == p.current {
			mark = "● "
		}
		fold := ""
		if row.HasChildren {
			fold = "▾ "
			if p.collapsed[row.Item.ID] {
				fold = "▸ "
			}
		}
		text := row.Prefix + fold + row.Item.Kind + " " + row.Item.Text
		if row.Item.Label != "" {
			text = row.Prefix + fold + "[" + row.Item.Label + "] " + row.Item.Kind + " " + row.Item.Text
		}
		style := th.Foreground
		if index == p.Selected {
			style = th.SelectionFg
			style.Bg = th.SelectionBg.Bg
			style.Reverse = true
		}
		offset := p.horizontal
		if p.Selected < len(p.rows) {
			offset += max(0, xui.StringWidth(p.rows[p.Selected].Prefix, ctx.Method)-max(w/3, 2))
		}
		print(i+2, mark+viewport(singleLine(text), offset, ctx.Method), style)
	}
	for i := range helpRows {
		print(h-1-helpRows+i, help[i], th.Muted)
	}
	item := p.selected()
	caption := fmt.Sprintf(" %d/%d ", p.Selected+1, len(p.rows))
	if !item.Time.IsZero() {
		caption += item.Time.Format("2006-01-02 15:04")
	}
	if !item.LabelTime.IsZero() {
		caption += " · label " + item.LabelTime.Format("2006-01-02 15:04")
	}
	if p.Status != "" {
		caption = p.Status
	}
	print(h-1, caption, th.Muted)
	return surface
}

func viewport(text string, columns int, method xui.WidthMethod) string {
	for columns > 0 && text != "" {
		_, n := utf8.DecodeRuneInString(text)
		columns -= xui.StringWidth(text[:n], method)
		text = text[n:]
	}
	return text
}
