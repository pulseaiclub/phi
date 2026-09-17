package diffpane

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/diffview"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/util/diffreview"
)

// Pane is a full-screen git diff review overlay.
type Pane struct {
	theme components.Theme
	cwd   string

	active bool
	spec   []string
	label  string
	err    string

	rows        []diffreview.Row
	drafts      []diffreview.CommentDraft
	commentPath string
	highlighted map[int][]components.Span

	cursor     int
	scroll     int
	xScroll    int
	sideBySide bool
	viewH      int

	pendingG       bool
	pendingBracket rune

	searchMode    bool
	searchQuery   string
	searchMatches []int
	searchIndex   int

	commentEdit   bool
	commentBody   string
	commentCur    int
	commentTarget diffreview.CommentDraft

	help   bool
	status string
	dirty  bool

	picker listpicker.Picker

	onSubmit func(text string)
	onCopy   func(text string) bool
	onToast  func(msg string)
}

// New builds an inactive review pane.
func New(
	theme components.Theme,
	cwd string,
	onSubmit func(string),
	onCopy func(string) bool,
	onToast func(string),
) *Pane {
	p := &Pane{
		theme:    theme,
		cwd:      cwd,
		onSubmit: onSubmit,
		onCopy:   onCopy,
		onToast:  onToast,
		picker:   listpicker.Picker{Theme: theme},
	}
	p.picker.OnAccept = p.acceptPicker
	return p
}

// Active reports whether the overlay is showing.
func (p *Pane) Active() bool { return p != nil && p.active }

// SetTheme updates chrome and syntax colors.
func (p *Pane) SetTheme(th components.Theme) {
	if p == nil {
		return
	}
	p.theme = th
	p.picker.Theme = th
	p.highlighted = diffview.HighlightRows(p.rows, th)
}

// OpenGit loads a git diff in cwd and shows the overlay.
func (p *Pane) OpenGit(cwd string, spec []string) {
	if p == nil {
		return
	}
	if cwd != "" {
		p.cwd = cwd
	}
	p.spec = append([]string(nil), spec...)
	p.label = diffreview.LabelForSpec(spec)
	text, err := diffreview.LoadGit(context.Background(), p.cwd, spec)
	if err != nil {
		p.openParsed("", err.Error())
		return
	}
	p.openParsed(text, "")
}

// OpenText shows a preloaded unified diff (tests / paste).
func (p *Pane) OpenText(input string, spec []string) {
	if p == nil {
		return
	}
	p.spec = append([]string(nil), spec...)
	p.label = diffreview.LabelForSpec(spec)
	p.openParsed(input, "")
}

func (p *Pane) openParsed(input, loadErr string) {
	p.active = true
	p.err = loadErr
	p.resetUI()
	p.pendingG = false
	p.pendingBracket = 0
	p.commentPath = diffreview.CommentPath(p.cwd)

	if loadErr != "" {
		p.rows = nil
		p.drafts = nil
		p.highlighted = nil
		p.cursor = 0
		p.scroll = 0
		p.status = loadErr
		return
	}

	doc, err := diffreview.Parse(input)
	if err != nil {
		p.rows = nil
		p.status = err.Error()
		return
	}
	p.rows = doc.Rows()
	file, err := diffreview.LoadFile(p.commentPath)
	if err != nil {
		p.status = "comments: " + err.Error()
		p.drafts = nil
	} else {
		p.drafts = file.Comments
	}
	p.highlighted = diffview.HighlightRows(p.rows, p.theme)
	p.cursor = 0
	p.scroll = 0
	p.xScroll = 0
	p.clampCursor()
	if len(p.rows) == 0 {
		p.status = "empty diff"
	} else {
		p.status = fmt.Sprintf("%d files", countFiles(p.rows))
	}
}

// Close hides the overlay, saving comments first.
func (p *Pane) Close() {
	if p == nil {
		return
	}
	if p.dirty {
		_ = p.saveComments()
	}
	p.active = false
	p.resetUI()
}

// Handle consumes keyboard input while the overlay is active.
func (p *Pane) Handle(ctx *components.EventContext, ev xui.Event) {
	if p == nil || !p.active {
		return
	}
	switch e := ev.(type) {
	case xui.KeyEvent:
		if !e.Press {
			return
		}
		p.handleKey(ctx, e)
	default:
		ctx.Consume = true
	}
}

func (p *Pane) handleKey(ctx *components.EventContext, e xui.KeyEvent) {
	if p.picker.Open {
		p.picker.Handle(ctx, e)
		return
	}
	if p.commentEdit {
		p.handleCommentKey(ctx, e)
		return
	}
	if p.searchMode {
		p.handleSearchKey(ctx, e)
		return
	}
	if p.help {
		p.help = false
		ctx.ConsumeAndRedraw()
		if e.Code == xui.KeyEscape || (e.Code == xui.KeyRune && e.Rune == '?') {
			return
		}
	}

	if e.Code == xui.KeyEscape ||
		(e.Code == xui.KeyRune && (e.Rune == 'q' || e.Rune == 'Q') && !e.Mods.Has(xui.ModCtrl)) {
		p.Close()
		ctx.ConsumeAndRedraw()
		return
	}

	if p.handlePending(ctx, e) {
		return
	}

	switch e.Code {
	case xui.KeyUp:
		p.moveCursor(-1)
	case xui.KeyDown:
		p.moveCursor(1)
	case xui.KeyLeft:
		p.xScroll = max(0, p.xScroll-4)
	case xui.KeyRight:
		p.xScroll += 4
	case xui.KeyPageUp:
		p.moveCursor(-p.page())
	case xui.KeyPageDown:
		p.moveCursor(p.page())
	case xui.KeyHome:
		p.setCursor(0)
	case xui.KeyEnd:
		p.setCursor(len(p.rows) - 1)
	case xui.KeyRune:
		if e.Mods.Has(xui.ModCtrl) {
			switch e.Rune {
			case 'd', 'D':
				p.moveCursor(p.page())
			case 'u', 'U':
				p.moveCursor(-p.page())
			}
			ctx.ConsumeAndRedraw()
			return
		}
		p.handleRune(ctx, e.Rune)
		return
	default:
		ctx.Consume = true
		return
	}
	ctx.ConsumeAndRedraw()
}

func (p *Pane) handlePending(ctx *components.EventContext, e xui.KeyEvent) bool {
	if p.pendingG {
		p.pendingG = false
		if e.Code == xui.KeyRune && e.Rune == 'g' {
			p.setCursor(0)
			ctx.ConsumeAndRedraw()
			return true
		}
	}
	if p.pendingBracket != 0 {
		br := p.pendingBracket
		p.pendingBracket = 0
		if e.Code == xui.KeyRune {
			dir := 1
			if br == '[' {
				dir = -1
			}
			switch e.Rune {
			case 'c', 'C':
				p.jumpChange(dir)
			case 'n', 'N':
				p.jumpNote(dir)
			}
			ctx.ConsumeAndRedraw()
			return true
		}
	}
	return false
}

func (p *Pane) handleRune(ctx *components.EventContext, r rune) {
	switch r {
	case 'j':
		p.moveCursor(1)
	case 'k':
		p.moveCursor(-1)
	case 'h':
		p.xScroll = max(0, p.xScroll-4)
	case 'l':
		p.xScroll += 4
	case 'g':
		p.pendingG = true
	case 'G':
		p.setCursor(len(p.rows) - 1)
	case 'J':
		p.jumpFile(1)
	case 'K':
		p.jumpFile(-1)
	case ']':
		p.pendingBracket = ']'
	case '[':
		p.pendingBracket = '['
	case 's':
		p.sideBySide = !p.sideBySide
		p.revealCursor()
	case '/':
		p.searchMode = true
		p.searchQuery = ""
		p.searchMatches = nil
	case 'n':
		p.moveSearch(1)
	case 'N':
		p.moveSearch(-1)
	case 'i', 'I':
		p.openCommentEditor()
	case 'x':
		p.deleteNote()
	case 'a':
		p.sendToAgent()
	case 'y':
		p.copyLine()
	case 'f':
		p.openFilePicker()
	case 'r':
		p.OpenGit(p.cwd, p.spec)
	case '?':
		p.help = true
	default:
		ctx.Consume = true
		return
	}
	ctx.ConsumeAndRedraw()
}

func (p *Pane) handleSearchKey(ctx *components.EventContext, e xui.KeyEvent) {
	switch e.Code {
	case xui.KeyEscape:
		p.searchMode = false
		p.searchQuery = ""
		p.searchMatches = nil
	case xui.KeyEnter:
		p.searchMode = false
	case xui.KeyBackspace:
		if p.searchQuery != "" {
			runes := []rune(p.searchQuery)
			p.searchQuery = string(runes[:len(runes)-1])
			p.updateSearch()
		}
	case xui.KeyRune:
		if e.Mods.Has(xui.ModCtrl) || e.Mods.Has(xui.ModAlt) {
			break
		}
		if e.Rune >= 0x20 {
			p.searchQuery += string(e.Rune)
			p.updateSearch()
		}
	}
	ctx.ConsumeAndRedraw()
}

func (p *Pane) handleCommentKey(ctx *components.EventContext, e xui.KeyEvent) {
	switch e.Code {
	case xui.KeyEscape:
		p.commentEdit = false
	case xui.KeyEnter:
		if e.Mods.Has(xui.ModShift) {
			p.insertComment("\n")
			break
		}
		p.submitComment()
	case xui.KeyBackspace:
		if p.commentCur > 0 {
			_, size := utf8.DecodeLastRuneInString(p.commentBody[:p.commentCur])
			p.commentBody = p.commentBody[:p.commentCur-size] + p.commentBody[p.commentCur:]
			p.commentCur -= size
		}
	case xui.KeyLeft:
		if p.commentCur > 0 {
			_, size := utf8.DecodeLastRuneInString(p.commentBody[:p.commentCur])
			p.commentCur -= size
		}
	case xui.KeyRight:
		if p.commentCur < len(p.commentBody) {
			_, size := utf8.DecodeRuneInString(p.commentBody[p.commentCur:])
			p.commentCur += size
		}
	case xui.KeyRune:
		if e.Mods.Has(xui.ModCtrl) || e.Mods.Has(xui.ModAlt) {
			break
		}
		if e.Rune >= 0x20 {
			p.insertComment(string(e.Rune))
		}
	}
	ctx.ConsumeAndRedraw()
}

func (p *Pane) insertComment(s string) {
	p.commentBody = p.commentBody[:p.commentCur] + s + p.commentBody[p.commentCur:]
	p.commentCur += len(s)
}

func (p *Pane) openCommentEditor() {
	if p.cursor < 0 || p.cursor >= len(p.rows) {
		p.toast("no line to comment")
		return
	}
	row := p.rows[p.cursor]
	idx := diffreview.BuildCommentIndex(p.rows, p.drafts)
	if existing := idx.DraftsForRow(p.cursor); len(existing) > 0 {
		p.commentTarget = existing[0]
		p.commentBody = existing[0].Body
		p.commentCur = len(p.commentBody)
		p.commentEdit = true
		return
	}
	if !diffreview.AnchorValid(row.Review) {
		p.toast("cannot comment on this line")
		return
	}
	p.commentTarget = diffreview.DraftFromAnchor(row.Review)
	p.commentBody = ""
	p.commentCur = 0
	p.commentEdit = true
}

func (p *Pane) submitComment() {
	body := strings.TrimSpace(p.commentBody)
	p.commentEdit = false
	if body == "" {
		p.removeDraft(p.commentTarget)
		p.dirty = true
		if err := p.saveComments(); err != nil {
			p.toast(err.Error())
		}
		return
	}
	found := false
	for i, d := range p.drafts {
		if diffreview.SameTarget(d, p.commentTarget) || (d.ID != "" && d.ID == p.commentTarget.ID) {
			p.drafts[i].Body = body
			found = true
			break
		}
	}
	if !found {
		d := p.commentTarget
		d.Body = body
		if d.ID == "" {
			d.ID = strconv.FormatInt(time.Now().UnixNano(), 36)
		}
		p.drafts = append(p.drafts, d)
	}
	p.persistComments("note saved")
}

func (p *Pane) deleteNote() {
	idx := diffreview.BuildCommentIndex(p.rows, p.drafts)
	drafts := idx.DraftsForRow(p.cursor)
	if len(drafts) == 0 {
		p.toast("no note on this line")
		return
	}
	p.removeDraft(drafts[0])
	p.persistComments("note deleted")
}

func (p *Pane) removeDraft(target diffreview.CommentDraft) {
	out := p.drafts[:0]
	for _, d := range p.drafts {
		if diffreview.SameTarget(d, target) || (target.ID != "" && d.ID == target.ID) {
			continue
		}
		out = append(out, d)
	}
	p.drafts = out
}

func (p *Pane) saveComments() error {
	if p.commentPath == "" {
		return nil
	}
	err := diffreview.SaveFile(p.commentPath, diffreview.CommentFile{Version: 1, Comments: p.drafts})
	if err == nil {
		p.dirty = false
	}
	return err
}

func (p *Pane) resetUI() {
	p.help = false
	p.searchMode = false
	p.commentEdit = false
	p.picker.Hide()
}

func (p *Pane) persistComments(status string) {
	p.dirty = true
	if err := p.saveComments(); err != nil {
		p.toast(err.Error())
		return
	}
	p.status = status
}

func (p *Pane) sendToAgent() {
	prompt := strings.TrimSpace(diffreview.FormatPrompt(p.drafts))
	if prompt == "" {
		p.toast("no notes to send")
		return
	}
	p.Close()
	if p.onSubmit != nil {
		p.onSubmit(prompt)
	}
}

func (p *Pane) copyLine() {
	if p.cursor < 0 || p.cursor >= len(p.rows) {
		return
	}
	text := p.rows[p.cursor].Code
	if text == "" {
		text = p.rows[p.cursor].Text
	}
	if p.onCopy != nil && p.onCopy(text) {
		p.toast("copied")
	}
}

func (p *Pane) openFilePicker() {
	items := make([]listpicker.Item, 0)
	seen := map[string]bool{}
	for i, row := range p.rows {
		if row.Kind != diffreview.RowFile {
			continue
		}
		id := row.Text
		if seen[id] {
			continue
		}
		seen[id] = true
		items = append(items, listpicker.Item{
			ID:      strconv.Itoa(i),
			Primary: row.Text,
			Detail:  row.FileName,
		})
	}
	if len(items) == 0 {
		p.toast("no files in diff")
		return
	}
	p.picker.Show(items, listpicker.ShowConfig{
		Title:      "Files",
		FilterHint: "filter files",
		Empty:      "no files",
	})
}

func (p *Pane) acceptPicker(item listpicker.Item) {
	row, _ := strconv.Atoi(item.ID)
	p.setCursor(row)
}

func (p *Pane) updateSearch() {
	q := strings.ToLower(p.searchQuery)
	p.searchMatches = p.searchMatches[:0]
	if q == "" {
		return
	}
	for i, row := range p.rows {
		hay := strings.ToLower(row.Code + " " + row.Text + " " + row.FileName)
		if strings.Contains(hay, q) {
			p.searchMatches = append(p.searchMatches, i)
		}
	}
	if len(p.searchMatches) > 0 {
		p.searchIndex = 0
		p.setCursor(p.searchMatches[0])
	}
}

func (p *Pane) moveSearch(dir int) {
	if len(p.searchMatches) == 0 {
		if p.searchQuery != "" {
			p.updateSearch()
		}
		if len(p.searchMatches) == 0 {
			p.toast("no matches")
			return
		}
	}
	p.searchIndex = (p.searchIndex + dir) % len(p.searchMatches)
	if p.searchIndex < 0 {
		p.searchIndex = len(p.searchMatches) - 1
	}
	p.setCursor(p.searchMatches[p.searchIndex])
}

func (p *Pane) jumpFile(dir int) {
	p.jumpKind(dir, diffreview.RowFile)
}

func (p *Pane) jumpChange(dir int) {
	start := p.cursor
	n := len(p.rows)
	if n == 0 {
		return
	}
	for step := 1; step <= n; step++ {
		i := start + dir*step
		if i < 0 {
			i += n
		}
		if i >= n {
			i -= n
		}
		kind := p.rows[i].Kind
		if kind != diffreview.RowAdd && kind != diffreview.RowDelete {
			continue
		}
		prev := diffreview.RowBlank
		if i > 0 {
			prev = p.rows[i-1].Kind
		}
		if prev != diffreview.RowAdd && prev != diffreview.RowDelete {
			p.setCursor(i)
			return
		}
	}
}

func (p *Pane) jumpNote(dir int) {
	targets := diffreview.BuildCommentIndex(p.rows, p.drafts).TargetRows()
	if len(targets) == 0 {
		p.toast("no notes")
		return
	}
	next := targets[len(targets)-1]
	if dir > 0 {
		next = targets[0]
		for _, t := range targets {
			if t > p.cursor {
				next = t
				break
			}
		}
		if next <= p.cursor {
			next = targets[0]
		}
	} else {
		for i := range slices.Backward(targets) {
			if targets[i] < p.cursor {
				next = targets[i]
				break
			}
		}
		if next >= p.cursor {
			next = targets[len(targets)-1]
		}
	}
	p.setCursor(next)
}

func (p *Pane) jumpKind(dir int, kind diffreview.RowKind) {
	start := p.cursor
	n := len(p.rows)
	if n == 0 {
		return
	}
	for step := 1; step <= n; step++ {
		i := start + dir*step
		if i < 0 {
			i += n
		}
		if i >= n {
			i -= n
		}
		if p.rows[i].Kind == kind {
			p.setCursor(i)
			return
		}
	}
}

func (p *Pane) page() int {
	h := p.viewH / 2
	if h < 1 {
		h = 8
	}
	return h
}

func (p *Pane) moveCursor(delta int) {
	p.setCursor(p.cursor + delta)
}

func (p *Pane) setCursor(i int) {
	p.cursor = i
	p.clampCursor()
	p.revealCursor()
}

func (p *Pane) clampCursor() {
	if len(p.rows) == 0 {
		p.cursor = 0
		return
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
	if p.cursor >= len(p.rows) {
		p.cursor = len(p.rows) - 1
	}
}

func (p *Pane) revealCursor() {
	vis := p.visList()
	idx := 0
	for i, v := range vis {
		if v.Kind == diffview.VisRow && v.Doc == p.cursor {
			idx = i
			break
		}
	}
	if idx < p.scroll {
		p.scroll = idx
	}
	bottom := p.scroll + p.viewH - 1
	if p.viewH < 1 {
		bottom = p.scroll + 16
	}
	if idx > bottom {
		p.scroll = idx - max(p.viewH-1, 1)
	}
	if p.scroll < 0 {
		p.scroll = 0
	}
}

func (p *Pane) visList() []diffview.Vis {
	idx := diffreview.BuildCommentIndex(p.rows, p.drafts)
	if p.sideBySide {
		return p.visSide(idx)
	}
	out := make([]diffview.Vis, 0, len(p.rows)+len(p.drafts))
	for i := range p.rows {
		out = append(out, diffview.Vis{Kind: diffview.VisRow, Doc: i, Active: i == p.cursor})
		for _, d := range idx.DraftsForRow(i) {
			out = append(out, noteVis(i, i == p.cursor, d.Body))
		}
	}
	return out
}

func (p *Pane) visSide(idx diffreview.CommentIndex) []diffview.Vis {
	sides := diffreview.SideBySideRows(p.rows)
	out := make([]diffview.Vis, 0, len(sides)+len(p.drafts))
	for _, side := range sides {
		doc := side.FirstDoc()
		active := side.ContainsDoc(p.cursor)
		if active {
			doc = p.cursor
		}
		s := side
		out = append(out, diffview.Vis{Kind: diffview.VisRow, Doc: doc, Side: &s, Active: active})
		seen := map[string]bool{}
		for _, docRow := range []int{side.Full, side.Left, side.Right} {
			for _, d := range idx.DraftsForRow(docRow) {
				key := d.Path + strconv.Itoa(d.Line) + string(d.Side) + d.Body
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, noteVis(docRow, active, d.Body))
			}
		}
	}
	return out
}

// Draw paints the overlay.
func (p *Pane) Draw(ctx components.DrawContext) components.Surface {
	if p == nil {
		return components.NewSurface(ctx.Max.Width, ctx.Max.Height, nil)
	}
	p.viewH = max(ctx.Max.Height-2, 1)
	p.revealCursor()

	prompt, kind := "", ""
	cur := 0
	if p.commentEdit {
		kind, prompt, cur = "note", p.commentBody, p.commentCur
	} else if p.searchMode {
		kind, prompt = "/", p.searchQuery
	}

	status := p.status
	if p.err != "" {
		status = p.err
	} else if loc := p.location(); loc != "" {
		status = loc
	}

	surf := diffview.Paint(ctx, diffview.Model{
		Theme:       p.theme,
		Title:       "diff · " + p.label,
		Status:      status,
		Rows:        p.rows,
		Vis:         p.visList(),
		Scroll:      p.scroll,
		XScroll:     p.xScroll,
		SideBySide:  p.sideBySide,
		Help:        p.help,
		Prompt:      prompt,
		PromptKind:  kind,
		CursorByte:  cur,
		Highlighted: p.highlighted,
		Empty:       emptyMessage(p.err, len(p.rows), p.spec),
	})
	if p.picker.Open {
		if overlay, ok := p.pickerOverlay(ctx); ok {
			surf.Children = append(surf.Children, overlay)
		}
	}
	return surf
}

func (p *Pane) pickerOverlay(ctx components.DrawContext) (components.SubSurface, bool) {
	return components.SubSurface{
		Origin:  components.Point{X: 0, Y: 0},
		Z:       20,
		Surface: p.picker.Draw(ctx),
	}, true
}

func (p *Pane) location() string {
	if p.cursor < 0 || p.cursor >= len(p.rows) {
		return ""
	}
	row := p.rows[p.cursor]
	file := row.FileName
	if file == "" {
		file = row.Text
	}
	n := row.Review.Line
	notes := len(diffreview.BuildCommentIndex(p.rows, p.drafts).TargetRows())
	loc := file
	if n > 0 {
		loc = fmt.Sprintf("%s:%d", file, n)
	}
	if notes > 0 {
		loc = fmt.Sprintf("%s · %d notes", loc, notes)
	}
	if p.sideBySide {
		loc += " · split"
	}
	return loc
}

func (p *Pane) toast(msg string) {
	p.status = msg
	if p.onToast != nil {
		p.onToast(msg)
	}
}

func countFiles(rows []diffreview.Row) int {
	n := 0
	for _, r := range rows {
		if r.Kind == diffreview.RowFile {
			n++
		}
	}
	return n
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

func noteVis(doc int, active bool, body string) diffview.Vis {
	return diffview.Vis{Kind: diffview.VisNote, Doc: doc, Active: active, Note: oneLine(body)}
}

func emptyMessage(loadErr string, n int, spec []string) string {
	if loadErr != "" {
		return loadErr
	}
	if n == 0 {
		return diffreview.EmptyNote(spec)
	}
	return ""
}
