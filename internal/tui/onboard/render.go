package onboard

import (
	"fmt"
	"strings"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/layout"
	"github.com/pulseaiclub/phi/internal/project/model"
)

const (
	// panelWidth keeps the two input rows and their hints on one line each.
	panelWidth = 64
	// labelWidth aligns both field values in one column.
	labelWidth = len("api key")
)

// Draw paints a blank root with the dialog centered on top, the same layering
// the config editor's confirm modal uses.
func (d *Dialog) Draw(ctx components.DrawContext) components.Surface {
	w, h := ctx.Max.Width, ctx.Max.Height
	root := components.NewSurface(w, h, d)
	fill := xui.Style{}
	for y := range h {
		for x := range w {
			root.SetCell(x, y, xui.Cell{Char: " ", Width: 1, Style: fill})
		}
	}

	width := min(panelWidth, max(w-4, 30))
	panel := d.drawPanel(width, ctx.Method)
	root.Children = append(root.Children, components.SubSurface{
		Origin:  components.Point{X: max((w-width)/2, 0), Y: max((h-panel.Size.Height)/2, 0)},
		Z:       30,
		Surface: panel,
	})
	return root
}

// drawPanel paints the dialog box and records the focused caret position.
func (d *Dialog) drawPanel(width int, method xui.WidthMethod) components.Surface {
	th := d.theme
	bodyW := width - 4

	var lines []components.RichLine
	add := func(spans ...components.Span) {
		lines = append(lines, components.WrapSpans(spans, bodyW, method)...)
	}
	add(components.Span{Text: "Set up phi", Style: chrome.PanelTitle(th)})
	add(components.Span{
		Text:  "A model and its API key are all phi needs — other fields",
		Style: th.Muted,
	})
	add(components.Span{
		Text:  "come from the model preset. Adjust later with phi config.",
		Style: th.Muted,
	})
	lines = append(lines, components.RichLine{})

	// Field rows are painted below from the dialog state, not as wrapped text:
	// each owns the caret when focused.
	fieldsTop := len(lines)
	lines = append(lines,
		components.RichLine{}, // name row
		components.RichLine{}, // key row
		components.RichLine{},
	)

	for _, h := range presetHint(d.Name()) {
		style := th.Muted
		if h.warn {
			style = th.Warning
		}
		add(components.Span{Text: h.text, Style: style})
	}
	if d.err != "" {
		add(components.Span{Text: d.err, Style: th.Destructive})
	}
	add(components.Span{Text: chrome.AskHint("tab field", "save", "quit"), Style: th.Muted})

	height := len(lines) + 2
	panel := components.NewSurface(width, height, d)
	for y := range height {
		for x := range width {
			panel.SetCell(x, y, xui.Cell{Char: " ", Width: 1, Style: xui.Style{Fg: th.Foreground.Fg}})
		}
	}
	layout.DrawRoundedBorder(&panel, layout.BorderRounded, chrome.ModalBorder(th), nil, nil, nil, nil, method)
	for i, line := range lines {
		// The field rows are painted by drawFields, which owns their caret.
		if i >= fieldsTop && i < fieldsTop+fieldCount {
			continue
		}
		components.PaintSpans(&panel, 2, i+1, line, method)
	}
	d.drawFields(&panel, fieldsTop+1, bodyW, method)
	panel.Cursor = &components.Point{X: d.caretX, Y: d.caretY}
	return panel
}

// drawFields paints the two input rows and the caret of the focused one.
func (d *Dialog) drawFields(panel *components.Surface, top, bodyW int, method xui.WidthMethod) {
	th := d.theme
	bg := th.SelectionBg.Bg
	valueX := 2 + labelWidth + 2
	rows := [fieldCount]struct {
		label  string
		text   string
		filled bool
	}{
		fieldName: {label: "model", text: d.name.Text(), filled: d.Name() != ""},
		fieldKey:  {label: "api key", text: maskInput(d.key.Text()), filled: d.APIKey() != ""},
	}
	for i, row := range rows {
		y := top + i
		focused := d.focus == i
		style := th.Muted
		if focused {
			for x := 1; x < panel.Size.Width-1; x++ {
				panel.SetCell(x, y, xui.Cell{Char: " ", Width: 1, Style: xui.Style{Bg: bg}})
			}
			style = th.SelectionFg
			style.Bg = bg
			style.Bold = true
		}
		panel.Print(2, y, row.label, style, method)
		value := row.text
		valueStyle := style
		if !row.filled && !focused {
			valueStyle = th.Muted
			valueStyle.Dim = true
			if value == "" {
				if i == fieldName {
					value = "e.g. deepseek-flash"
				} else {
					value = "paste key"
				}
			}
		}
		value = components.TruncateToWidth(value, max(panel.Size.Width-2-valueX, 1), method)
		panel.Print(valueX, y, value, valueStyle, method)
		if focused {
			var before string
			if i == fieldName {
				before, _ = d.name.Split()
			} else {
				before, _ = d.key.Split()
				before = maskInput(before)
			}
			d.caretX = valueX + xui.StringWidth(before, method)
			d.caretY = y
		}
	}
}

// maskInput hides a key being typed. Unlike the config list it keeps no
// preview characters: this is an input field, not an overview.
func maskInput(value string) string {
	return strings.Repeat("•", len([]rune(value)))
}

// hintLine is one summary row under the fields; warn paints it in the
// attention color instead of the quiet tone.
type hintLine struct {
	text string
	warn bool
}

// presetHint summarizes what the loader would derive from the name, so an
// unknown or gateway-only name is called out before the first request fails.
// Lines stay short enough for one panel row each: WrapSpans breaks mid-word.
func presetHint(name string) []hintLine {
	if name == "" {
		return nil
	}
	p, ok := model.Lookup(name)
	switch {
	case !ok:
		return []hintLine{
			{text: "unknown name: requests default to api.openai.com", warn: true},
			{text: "gateways need base_url — use phi config"},
		}
	case p.Config.BaseURL == "":
		return []hintLine{
			{text: "gateway route: capabilities only, no connection", warn: true},
			{text: "set base_url with phi config before use"},
		}
	default:
		return []hintLine{{text: fmt.Sprintf("preset: %s · %s", p.Config.API, p.Config.BaseURL)}}
	}
}
