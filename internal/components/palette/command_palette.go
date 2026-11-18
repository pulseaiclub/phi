package palette

import (
	"strings"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
)

// PaletteCommand is one entry in the command palette.
type PaletteCommand struct {
	ID          string
	Noun        string // optional category used for filtering
	Verb        string // command label in the first column
	Description string // explanation in the second column
	Shortcut    string // e.g. "Ctrl+K"
	Keywords    []string
	Disabled    bool
	// Submenu opens a nested picker (e.g. Select Theme).
	Submenu      []PaletteCommand
	SubmenuTitle string // header for the nested list; default Verb
	// KeepOpen leaves the palette open after Run (when not using Submenu).
	KeepOpen bool
	Run      func()
	// OnSave handles Ctrl+S for the highlighted entry after confirmation;
	// success closes the picker.
	OnSave func() bool
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

// CommandPalette maps palette commands onto the shared list picker. It owns
// command behavior and nested pages; listpicker owns all rendering and input.
type CommandPalette struct {
	Title string
	// Root is the command registry shown on entry, replaced live by extension
	// syncs. Commands is the page on screen — a snapshot of Root or of a
	// pushed submenu. Keeping them apart means a sync under an open submenu
	// cannot orphan its rows: they still resolve against the page they came
	// from.
	Root     []PaletteCommand
	Commands []PaletteCommand
	Picker   listpicker.Picker

	stack []paletteFrame
}

type paletteFrame struct {
	title    string
	commands []PaletteCommand
	state    listpicker.State
}

func (p *CommandPalette) Show() {
	p.stack = nil
	p.show("Command Palette", p.Root)
}

// SetRoot replaces the root registry. A sync that lands while the root page
// is open refreshes it in place, keeping the query; an open submenu keeps
// running from its snapshot, so its rows never silently stop resolving.
func (p *CommandPalette) SetRoot(cmds []PaletteCommand) {
	p.Root = cmds
	if !p.Picker.Open || len(p.stack) > 0 {
		return
	}
	p.Commands = cmds
	p.Picker.SetItems(p.items(cmds))
}

func (p *CommandPalette) Hide() {
	p.stack = nil
	p.Picker.Hide()
}

func (p *CommandPalette) Push(title string, cmds []PaletteCommand) {
	if p.Picker.Open {
		p.stack = append(p.stack, paletteFrame{
			title: p.Title, commands: p.Commands, state: p.Picker.State(),
		})
	}
	p.show(title, cmds)
}

func (p *CommandPalette) Pop() bool {
	if len(p.stack) == 0 {
		return false
	}
	frame := p.stack[len(p.stack)-1]
	p.stack = p.stack[:len(p.stack)-1]
	p.show(frame.title, frame.commands)
	p.Picker.Restore(frame.state)
	return true
}

func (p *CommandPalette) show(title string, cmds []PaletteCommand) {
	p.Title, p.Commands = title, cmds
	p.Picker.OnAccept = p.accept
	p.Picker.OnEscape = p.Pop
	p.Picker.OnSave = p.save
	p.Picker.CanSave = func(item listpicker.Item) bool {
		cmd, ok := p.command(item.ID)
		return ok && !cmd.Disabled && cmd.OnSave != nil
	}
	p.Picker.OnClose = nil
	p.Picker.Show(p.items(cmds), p.config())
}

// items maps page commands onto picker rows.
func (*CommandPalette) items(cmds []PaletteCommand) []listpicker.Item {
	items := make([]listpicker.Item, 0, len(cmds))
	for _, cmd := range cmds {
		current, verb := splitMarker(cmd.Verb)
		detail := strings.TrimSpace(cmd.Description + " " + cmd.Shortcut)
		if cmd.Disabled {
			detail = strings.TrimSpace(detail + " unavailable")
		}
		items = append(items, listpicker.Item{
			ID: cmd.ID, Primary: verb, Detail: detail, Keywords: cmd.Label(), Current: current,
		})
	}
	return items
}

// config builds the picker chrome for a page; save copy rides on the page's
// own saveability.
func (p *CommandPalette) config() listpicker.ShowConfig {
	cfg := listpicker.ShowConfig{
		Title: p.Title, FilterHint: "filter commands…", Empty: "No commands",
		EmptyFilter: "No commands match %q", Hint: chrome.ListHint("run"),
		// The full caption folds at narrow widths; keep the save shortcut
		// alive there — a hidden ctrl+s reads as "saving unsupported".
		HintShort:      " ctrl+s save default ",
		ConfirmMessage: "Apply this model and save as global default?",
		ConfirmHint:    " y/enter save" + chrome.Sep + "n/esc cancel ",
		ConfirmShort:   " y save" + chrome.Sep + "n cancel ",
		TabNavigates:   true,
		Fuzzy:          true,
		Style:          listpicker.StyleCommand,
	}
	if p.saveAvailable() {
		cfg.Hint = chrome.ListHintExtra("run", "ctrl+s save default")
	}
	return cfg
}

func splitMarker(label string) (current bool, text string) {
	if after, found := strings.CutPrefix(label, chrome.CurrentMarker(true)); found {
		return true, after
	}
	return false, strings.TrimPrefix(label, chrome.CurrentMarker(false))
}

func (p *CommandPalette) command(id string) (PaletteCommand, bool) {
	for _, cmd := range p.Commands {
		if cmd.ID == id {
			return cmd, true
		}
	}
	return PaletteCommand{}, false
}

func (p *CommandPalette) accept(item listpicker.Item) {
	cmd, ok := p.command(item.ID)
	if !ok || cmd.Disabled {
		p.Picker.CancelAccept()
		return
	}
	if len(cmd.Submenu) > 0 {
		title := cmd.SubmenuTitle
		if title == "" {
			title = cmd.Verb
		}
		p.Push(title, cmd.Submenu)
		p.Picker.CancelAccept()
		return
	}
	depth := len(p.stack)
	if cmd.Run != nil {
		cmd.Run()
	}
	if len(p.stack) > depth || cmd.KeepOpen {
		p.Picker.CancelAccept()
	}
}

func (p *CommandPalette) save(item listpicker.Item) bool {
	cmd, ok := p.command(item.ID)
	return ok && !cmd.Disabled && cmd.OnSave != nil && cmd.OnSave()
}

// Draw delegates the complete overlay to the shared list picker. Keep the
// adapter as the surface widget so focus follows nested palette pages.
func (p *CommandPalette) Draw(ctx components.DrawContext) components.Surface {
	surface := p.Picker.Draw(ctx)
	surface.Widget = p
	return surface
}

func (p *CommandPalette) saveAvailable() bool {
	for _, cmd := range p.Commands {
		if !cmd.Disabled && cmd.OnSave != nil {
			return true
		}
	}
	return false
}

// Handle adds palette navigation around the shared picker.
func (p *CommandPalette) Handle(ctx *components.EventContext, ev xui.Event) {
	key, ok := ev.(xui.KeyEvent)
	if ok && key.Press && !p.Picker.IsConfirming() {
		if key.Mods.Has(xui.ModCtrl) && key.Code == xui.KeyRune && (key.Rune == 'k' || key.Rune == 'K') {
			p.Hide()
			ctx.ConsumeAndRedraw()
			return
		}
		if key.Code == xui.KeyEscape && p.Pop() {
			ctx.ConsumeAndRedraw()
			return
		}
	}
	p.Picker.Handle(ctx, ev)
}
