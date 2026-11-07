// Package onboard is the first-run dialog: when a fresh install has no usable
// model, phi starts a minimal terminal UI that collects just two values — the
// model name and its API key — and lets the preset machinery derive the rest.
// It runs before the main TUI boots, because the controller needs a usable
// model. Esc keeps the old advice (phi config / environment variables).
package onboard

import (
	"strings"
	"unicode"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/textfield"
)

// field indexes into the two input rows.
const (
	fieldName = iota
	fieldKey
	fieldCount
)

// Dialog asks for a model name and API key. It implements components.Widget
// and is meant to be the root widget of a short-lived app loop.
type Dialog struct {
	theme components.Theme

	name, key textfield.Field
	focus     int

	err string

	submitted bool
	aborted   bool

	// Painted geometry of the focused caret, recorded by Draw.
	caretX, caretY int
}

// New builds the dialog, prefilling the fields (e.g. from PHI_MODEL /
// PHI_API_KEY when only one of them was set).
func New(theme components.Theme, name, apiKey string) *Dialog {
	d := &Dialog{theme: theme}
	d.name.SetText(strings.TrimSpace(name))
	d.key.SetText(strings.TrimSpace(apiKey))
	return d
}

// Submitted reports whether the dialog finished with both values.
func (d *Dialog) Submitted() bool { return d != nil && d.submitted }

// Aborted reports whether the user backed out (Esc).
func (d *Dialog) Aborted() bool { return d != nil && d.aborted }

// Name returns the model name the user entered, trimmed.
func (d *Dialog) Name() string { return strings.TrimSpace(d.name.Text()) }

// APIKey returns the key the user entered, trimmed.
func (d *Dialog) APIKey() string { return strings.TrimSpace(d.key.Text()) }

// Handle routes one event. The dialog owns the keyboard outright: it is the
// whole screen until it submits or aborts.
func (d *Dialog) Handle(ctx *components.EventContext, ev xui.Event) {
	switch ev := ev.(type) {
	case xui.KeyEvent:
		if !ev.Press {
			return
		}
		d.handleKey(ctx, ev)
	case xui.PasteEvent:
		d.active().Insert(ev.Text)
		ctx.ConsumeAndRedraw()
	default:
		ctx.Consume = true
	}
}

func (d *Dialog) handleKey(ctx *components.EventContext, k xui.KeyEvent) {
	switch k.Code {
	case xui.KeyEnter:
		d.submit(ctx)
	case xui.KeyEscape:
		d.aborted = true
		ctx.Quit = true
		ctx.ConsumeAndRedraw()
	case xui.KeyTab, xui.KeyDown:
		d.focus = (d.focus + 1) % fieldCount
		d.err = ""
		ctx.ConsumeAndRedraw()
	case xui.KeyUp:
		d.focus = (d.focus + fieldCount - 1) % fieldCount
		d.err = ""
		ctx.ConsumeAndRedraw()

	case xui.KeyBackspace:
		d.active().DeleteBefore()
		ctx.ConsumeAndRedraw()
	case xui.KeyDelete:
		d.active().DeleteAt()
		ctx.ConsumeAndRedraw()
	case xui.KeyLeft:
		d.active().Move(-1)
		ctx.ConsumeAndRedraw()
	case xui.KeyRight:
		d.active().Move(1)
		ctx.ConsumeAndRedraw()
	case xui.KeyHome:
		d.active().Home()
		ctx.ConsumeAndRedraw()
	case xui.KeyEnd:
		d.active().End()
		ctx.ConsumeAndRedraw()

	case xui.KeyRune:
		if k.Mods.Has(xui.ModCtrl) {
			d.handleCtrl(k.Rune)
			ctx.ConsumeAndRedraw()
			return
		}
		if k.Mods.Has(xui.ModAlt) {
			ctx.Consume = true
			return
		}
		text := k.Text
		if text == "" && k.Rune >= 0x20 {
			text = string(k.Rune)
		}
		d.active().Insert(text)
		d.err = ""
		ctx.ConsumeAndRedraw()

	default:
		ctx.Consume = true
	}
}

// handleCtrl mirrors the readline edits the config form supports.
func (d *Dialog) handleCtrl(r rune) {
	line := d.active()
	switch unicode.ToLower(r) {
	case 'a':
		line.Home()
	case 'e':
		line.End()
	case 'u':
		line.KillBefore()
	case 'k':
		line.KillAfter()
	case 'w':
		// Rub out the word left of the caret (readline's word-rubout).
		for {
			ch, ok := line.PeekBefore()
			if !ok || !unicode.IsSpace(ch) {
				break
			}
			line.DeleteBefore()
		}
		for {
			ch, ok := line.PeekBefore()
			if !ok || unicode.IsSpace(ch) {
				break
			}
			line.DeleteBefore()
		}
	}
}

func (d *Dialog) active() *textfield.Field {
	if d.focus == fieldKey {
		return &d.key
	}
	return &d.name
}

// submit validates both values; on success it quits the loop with the values
// collected, on failure it points at the field to fix.
func (d *Dialog) submit(ctx *components.EventContext) {
	if d.Name() == "" {
		d.err, d.focus = "enter a model name", fieldName
		ctx.ConsumeAndRedraw()
		return
	}
	if d.APIKey() == "" {
		d.err, d.focus = "enter the API key", fieldKey
		ctx.ConsumeAndRedraw()
		return
	}
	d.err = ""
	d.submitted = true
	ctx.Quit = true
	ctx.ConsumeAndRedraw()
}
