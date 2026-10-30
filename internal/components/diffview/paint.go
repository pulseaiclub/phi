package diffview

import (
	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/layout"
	"github.com/pulseaiclub/phi/internal/util/diffreview"
)

// VisKind is one painted viewport line.
type VisKind int

const (
	VisRow VisKind = iota
	VisNote
)

// Vis is one viewport line produced by the review pane.
type Vis struct {
	Kind   VisKind
	Doc    int
	Side   *diffreview.SideBySideRow // nil in unified view
	Active bool
	Note   string
}

// Model is everything the painter needs for one frame.
type Model struct {
	Theme       components.Theme
	Title       string
	Status      string
	Hint        string
	Rows        []diffreview.Row
	Vis         []Vis
	Scroll      int
	XScroll     int
	SideBySide  bool
	Help        bool
	Prompt      string // search or comment chrome; empty = status row
	PromptKind  string // "/" or "note" or ""
	CursorByte  int    // comment editor cursor
	Highlighted map[int][]components.Span
	Empty       string
}

// Paint draws a full-screen diff review surface.
func Paint(ctx components.DrawContext, model Model) components.Surface {
	w, h := ctx.Max.Width, ctx.Max.Height
	if w < 1 {
		w = 80
	}
	if h < 1 {
		h = 24
	}
	s := components.NewSurface(w, h, nil)
	th := model.Theme
	method := ctx.Method

	fillRow(&s, 0, w, th.Title)
	title := model.Title
	if title == "" {
		title = "diff review"
	}
	s.Print(1, 0, components.TruncateToWidth(title, w-2, method), th.TitleOrForeground(), method)

	bodyH := max(h-2, 1)
	if len(model.Vis) == 0 && model.Empty != "" {
		s.Print(1, 2, components.TruncateToWidth(model.Empty, w-2, method), th.Muted, method)
	} else {
		y := 1
		for i := model.Scroll; i < len(model.Vis) && y < 1+bodyH; i++ {
			paintVis(&s, y, w, model, model.Vis[i], method)
			y++
		}
	}

	statusY := h - 1
	fillRow(&s, statusY, w, th.Muted)
	if model.PromptKind != "" {
		prefix := model.PromptKind + " "
		s.Print(0, statusY, prefix, th.ToolName, method)
		s.Print(
			len(prefix),
			statusY,
			components.TruncateToWidth(model.Prompt, w-len(prefix)-1, method),
			th.Foreground,
			method,
		)
		if model.PromptKind == "note" {
			col := len(prefix) + xui.StringWidth(model.Prompt[:min(model.CursorByte, len(model.Prompt))], method)
			if col < w {
				s.Cursor = &components.Point{X: col, Y: statusY}
			}
		}
	} else {
		left := model.Status
		right := model.Hint
		if right == "" {
			right = "j/k move" + chrome.Sep + "s split" + chrome.Sep + "i note" + chrome.Sep + "a send" + chrome.Sep + "? help" + chrome.Sep + "q quit"
		}
		s.Print(1, statusY, components.TruncateToWidth(left, w/2, method), th.Muted, method)
		rw := xui.StringWidth(right, method)
		if rw < w-2 {
			s.Print(w-1-rw, statusY, right, th.Muted, method)
		}
	}

	if model.Help {
		paintHelp(&s, th, method)
	}
	return s
}

func paintVis(s *components.Surface, y, w int, model Model, v Vis, method xui.WidthMethod) {
	th := model.Theme
	switch v.Kind {
	case VisNote:
		st := th.Warning
		if v.Active {
			st = withSel(st, th)
		}
		fillRow(s, y, w, st)
		s.Print(2, y, components.TruncateToWidth("▸ "+v.Note, w-3, method), st, method)
	default:
		if model.SideBySide && v.Side != nil && v.Side.Full < 0 {
			paintSideRow(s, y, w, model, v, method)
			return
		}
		doc := v.Doc
		if doc < 0 || doc >= len(model.Rows) {
			return
		}
		paintDocRow(s, y, w, model, model.Rows[doc], doc, v.Active, method)
	}
}

func paintSideRow(s *components.Surface, y, w int, model Model, v Vis, method xui.WidthMethod) {
	mid := w / 2
	if mid < 8 {
		paintDocRow(s, y, w, model, model.Rows[v.Doc], v.Doc, v.Active, method)
		return
	}
	side := *v.Side
	paintSideCell(
		s,
		y,
		0,
		mid,
		model,
		side.Left,
		v.Active && (side.Left == v.Doc || side.Left < 0 && side.Right != v.Doc),
		true,
		method,
	)
	s.SetCell(mid, y, xui.Cell{Char: "│", Width: 1, Style: model.Theme.Border})
	paintSideCell(
		s,
		y,
		mid+1,
		w-(mid+1),
		model,
		side.Right,
		v.Active && (side.Right == v.Doc || side.Right < 0),
		false,
		method,
	)
}

func paintSideCell(
	s *components.Surface,
	y, x, width int,
	model Model,
	doc int,
	active bool,
	left bool,
	method xui.WidthMethod,
) {
	if width <= 0 {
		return
	}
	th := model.Theme
	if doc < 0 || doc >= len(model.Rows) {
		if active {
			fillRange(s, x, y, width, th.SelectionBg)
		}
		return
	}
	row := model.Rows[doc]
	gutter := diffreview.SideGutter(model.Rows, row, left)
	paintCoded(s, y, x, width, model, row, doc, gutter, active, method)
}

func paintDocRow(
	s *components.Surface,
	y, w int,
	model Model,
	row diffreview.Row,
	doc int,
	active bool,
	method xui.WidthMethod,
) {
	switch row.Kind {
	case diffreview.RowFile, diffreview.RowHunk, diffreview.RowCommitHeader, diffreview.RowCommitMeta,
		diffreview.RowCommitMessage, diffreview.RowCommitTrailer, diffreview.RowPreamble,
		diffreview.RowDiffStat, diffreview.RowDiffStatSummary, diffreview.RowMeta, diffreview.RowBlank,
		diffreview.RowNoNewline:
		st := rowStyle(model.Theme, row.Kind)
		if active {
			st = withSel(st, model.Theme)
		}
		fillRow(s, y, w, st)
		text := row.Text
		if row.Kind == diffreview.RowFile {
			text = "▎ " + text
		}
		s.Print(0, y, components.TruncateToWidth(text, w, method), st, method)
	default:
		paintCoded(s, y, 0, w, model, row, doc, row.Gutter, active, method)
	}
}

func paintCoded(
	s *components.Surface,
	y, x, width int,
	model Model,
	row diffreview.Row,
	doc int,
	gutter string,
	active bool,
	method xui.WidthMethod,
) {
	th := model.Theme
	base := rowStyle(th, row.Kind)
	if active {
		base = withSel(base, th)
		fillRange(s, x, y, width, th.SelectionBg)
	}
	gw := xui.StringWidth(gutter, method)
	s.Print(x, y, gutter, gutterStyle(th, row.Kind), method)
	codeW := width - gw
	if codeW < 1 {
		return
	}
	spans := model.Highlighted[doc]
	if len(spans) == 0 {
		spans = []components.Span{{Text: row.Code, Style: base}}
	} else {
		spans = append([]components.Span(nil), spans...)
		if active {
			for i := range spans {
				spans[i].Style.Bg = th.SelectionBg.Bg
			}
		}
	}
	spans = applyInline(spans, row.Code, row.InlineSpans, inlineStyle(th, row.Kind))
	if model.XScroll > 0 {
		spans = skipCols(spans, model.XScroll, method)
	}
	components.PaintSpans(s, x+gw, y, clipSpans(spans, codeW, method), method)
}

func withSel(st xui.Style, th components.Theme) xui.Style {
	st.Bg = th.SelectionBg.Bg
	return st
}

func fillRow(s *components.Surface, y, w int, st xui.Style) {
	fillRange(s, 0, y, w, st)
}

func fillRange(s *components.Surface, x, y, w int, st xui.Style) {
	bg := xui.Style{Bg: st.Bg, Fg: st.Fg}
	for i := range w {
		s.SetCell(x+i, y, xui.Cell{Char: " ", Width: 1, Style: bg})
	}
}

func skipCols(spans []components.Span, cols int, method xui.WidthMethod) []components.Span {
	if cols <= 0 {
		return spans
	}
	skipped := 0
	// Once the cut lands inside a cluster we keep that cluster whole and stop
	// skipping; otherwise every later span loses its head and characters
	// vanish from the middle of the line.
	cut := false
	out := make([]components.Span, 0, len(spans))
	for _, sp := range spans {
		if cut {
			out = append(out, sp)
			continue
		}
		rest := sp.Text
		for rest != "" {
			cluster, cw, next := xui.FirstGrapheme(rest, method)
			rest = next
			if cw < 1 {
				cw = 1
			}
			if skipped+cw <= cols {
				skipped += cw
				continue
			}
			out = append(out, components.Span{Text: cluster + rest, Style: sp.Style})
			rest = ""
			cut = true
		}
	}
	return out
}

func clipSpans(spans []components.Span, width int, method xui.WidthMethod) []components.Span {
	if width <= 0 {
		return nil
	}
	var out []components.Span
	used := 0
	for _, sp := range spans {
		if used >= width {
			break
		}
		text := components.TruncateToWidth(sp.Text, width-used, method)
		if text == "" {
			break
		}
		out = append(out, components.Span{Text: text, Style: sp.Style})
		used += xui.StringWidth(text, method)
	}
	return out
}

func paintHelp(s *components.Surface, th components.Theme, method xui.WidthMethod) {
	lines := []string{
		"j/k  move          ]c/[c  change     i  add/edit note",
		"J/K  file          ]n/[n  note       x  delete note",
		"s    side-by-side  /      search     a  send to agent",
		"f    find file     n/N    next hit   r  refresh",
		"gg/G top/bottom    y      copy line",
		"q/Esc close        ?      this help",
	}
	w := 0
	for _, l := range lines {
		w = max(w, xui.StringWidth(l, method))
	}
	boxW := min(s.Size.Width-4, w+4)
	boxH := min(s.Size.Height-2, len(lines)+2)
	ox := max(0, (s.Size.Width-boxW)/2)
	oy := max(0, (s.Size.Height-boxH)/2)
	panel := components.NewSurface(boxW, boxH, nil)
	fill := xui.Style{Fg: th.Foreground.Fg}
	for y := range boxH {
		fillRange(&panel, 0, y, boxW, fill)
	}
	layout.DrawRoundedBorder(&panel, layout.BorderRounded, chrome.ModalBorder(th),
		&layout.BorderLabel{Text: " diff review ", Style: chrome.PanelTitle(th)}, nil, nil, nil, method)
	for i, l := range lines {
		if i+1 >= boxH-1 {
			break
		}
		panel.Print(2, i+1, components.TruncateToWidth(l, boxW-4, method), th.Foreground, method)
	}
	s.Children = append(s.Children, components.SubSurface{
		Origin:  components.Point{X: ox, Y: oy},
		Z:       10,
		Surface: panel,
	})
}
