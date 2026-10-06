// Package codepane renders a full-screen source-file pane: syntax-highlighted
// reading with a caret, a line-wise selection, and one handoff back to the chat
// composer.
package codepane

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chat"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/codeview"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/util"
)

const (
	// maxFileBytes keeps a stray multi-gigabyte log from freezing the reader.
	maxFileBytes = 8 << 20
	// binarySniffSize: a NUL byte in the head is the cheapest binary tell.
	binarySniffSize = 8 << 10
	// selLineWide marks the moving end of a line-wise selection. It only has to
	// exceed any real line, so the tint is clipped at the frame edge instead of
	// stopping mid-line.
	selLineWide = 1 << 20
)

// sensitivePaths is a snapshot of the gate's deny list. The viewer reads files
// itself, so it checks the same list rather than becoming the one hole in it.
var sensitivePaths = permission.SensitivePaths()

// Pane is a full-screen source-file overlay. Reading, key handling and painting
// all happen on the UI goroutine, so the state needs no lock.
type Pane struct {
	cwd     string
	onRef   func(chat.Ref)
	onToast func(string)

	theme  components.Theme
	method xui.WidthMethod

	active  bool
	abs     string
	rel     string
	lines   []string
	hl      map[int][]components.Span
	loadErr string

	line    int // 0-based cursor line
	col     int // byte offset into lines[line], always on a rune boundary
	scroll  int
	xScroll int
	viewW   int
	viewH   int

	pendingG bool

	// selecting is a line-wise visual selection anchored at selAnchor; the caret
	// (line) is the moving end, so plain movement extends it.
	selecting bool
	selAnchor int

	// A search is / then n/N. searchMode is the open prompt; searchQuery and
	// matches outlive it, because Enter commits the query and n/N repeat it.
	// searchFrom is where the prompt opened: narrowing a query must not walk
	// the caret down the file, and Esc hands the position back.
	searchMode  bool
	searchQuery string
	matches     []int
	searchFrom  caretPos
	// lowered is a lowercase copy of lines, built on the first search of a
	// file: scanning it costs a Contains per line, where lowercasing the file
	// again on every keystroke costs a ToLower per line.
	lowered []string

	// goto line
	gotoMode bool
	gotoBuf  string
}

// New builds an inactive pane. onRef receives the line the user picked for the
// chat input; onToast reports the outcome of an action.
func New(theme components.Theme, cwd string, onRef func(chat.Ref), onToast func(string)) *Pane {
	return &Pane{
		cwd:     cwd,
		onRef:   onRef,
		onToast: onToast,
		theme:   theme,
		method:  xui.WidthUnicode,
	}
}

// Active reports whether the overlay is showing.
func (p *Pane) Active() bool {
	return p.active
}

// SetTheme updates chrome and syntax colors.
func (p *Pane) SetTheme(th components.Theme) {
	p.theme = th
	p.hl = codeview.Highlight(p.abs, p.lines, th)
}

// Open shows a file from the top. Relative paths resolve against the pane cwd.
func (p *Pane) Open(path string) {
	p.OpenAt(path, 0)
}

// OpenAt shows path with the cursor on line (1-based; 0 leaves it at the top).
func (p *Pane) OpenAt(path string, line int) {
	p.active = true
	p.pendingG = false
	p.selecting = false
	p.resetPrompts()

	if err := p.load(path, line); err != nil {
		p.lines = nil
		p.hl = nil
		p.abs, p.rel = "", ""
		p.loadErr = loadMessage(p.cwd, err)
		p.line, p.col, p.scroll, p.xScroll = 0, 0, 0, 0
	}
}

// Close hides the overlay.
func (p *Pane) Close() {
	p.active = false
	p.pendingG = false
	p.resetPrompts()
}

// resetPrompts drops the transient / and : state. Open and Close both need it:
// the pane outlives a close, so a reopened file must not inherit a query.
func (p *Pane) resetPrompts() {
	p.searchMode = false
	p.searchQuery = ""
	p.matches = nil
	p.gotoMode = false
	p.gotoBuf = ""
}

// Handle consumes keyboard input while the overlay is active.
func (p *Pane) Handle(ctx *components.EventContext, ev xui.Event) {
	if !p.Active() {
		return
	}
	switch e := ev.(type) {
	case xui.KeyEvent:
		if !e.Press {
			return
		}
		if p.searchMode {
			p.handleSearchKey(ctx, e)
			return
		}
		if p.gotoMode {
			p.handleGotoKey(ctx, e)
			return
		}
		p.notify(p.handleKey(ctx, e))
	default:
		ctx.Consume = true
	}
}

func (p *Pane) handleSearchKey(ctx *components.EventContext, e xui.KeyEvent) {
	switch e.Code {
	case xui.KeyEscape:
		// Cancel: nothing was asked for, so the caret goes back to where the
		// prompt opened rather than staying on a match nobody picked.
		p.searchMode = false
		p.searchQuery = ""
		p.matches = nil
		p.jumpTo(p.searchFrom)
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

func (p *Pane) handleGotoKey(ctx *components.EventContext, e xui.KeyEvent) {
	switch e.Code {
	case xui.KeyEscape:
		p.gotoMode = false
		p.gotoBuf = ""
	case xui.KeyEnter:
		p.gotoMode = false
		// Atoi rejects the empty buffer, so a bare : is a no-op.
		if n, err := strconv.Atoi(p.gotoBuf); err == nil && n > 0 {
			p.jumpToLine(n - 1)
		}
		p.gotoBuf = ""
	case xui.KeyBackspace:
		if p.gotoBuf != "" {
			p.gotoBuf = p.gotoBuf[:len(p.gotoBuf)-1]
		}
	case xui.KeyRune:
		if e.Rune >= '0' && e.Rune <= '9' {
			p.gotoBuf += string(e.Rune)
		}
	}
	ctx.ConsumeAndRedraw()
}

// handleKey runs one key against pane state. It returns a toast to raise, which
// the caller delivers once the key is done with.
func (p *Pane) handleKey(ctx *components.EventContext, e xui.KeyEvent) string {
	if e.Code == xui.KeyEscape && p.selecting {
		// Cancel the selection first: losing a ten-line pick to a close that was
		// meant to undo it is worse than one extra Esc.
		p.selecting = false
		ctx.ConsumeAndRedraw()
		return ""
	}
	if e.Code == xui.KeyEscape ||
		(e.Code == xui.KeyRune && (e.Rune == 'q' || e.Rune == 'Q') && !e.Mods.Has(xui.ModCtrl)) {
		p.active = false
		p.selecting = false // a reopened pane starts clean
		p.pendingG = false
		ctx.ConsumeAndRedraw()
		return ""
	}
	if p.handlePending(ctx, e) {
		return ""
	}

	switch e.Code {
	case xui.KeyUp:
		p.moveLine(-1)
	case xui.KeyDown:
		p.moveLine(1)
	case xui.KeyLeft:
		p.moveCol(-1)
	case xui.KeyRight:
		p.moveCol(1)
	case xui.KeyPageUp:
		p.moveLine(-p.page())
	case xui.KeyPageDown:
		p.moveLine(p.page())
	case xui.KeyHome:
		p.col = 0
	case xui.KeyEnd:
		p.col = len(p.lineText())
	case xui.KeyRune:
		if e.Mods.Has(xui.ModCtrl) {
			switch e.Rune {
			case 'd', 'D':
				p.moveLine(p.page())
			case 'u', 'U':
				p.moveLine(-p.page())
			}
			ctx.ConsumeAndRedraw()
			return ""
		}
		return p.handleRune(ctx, e.Rune)
	default:
		ctx.Consume = true
		return ""
	}
	p.clamp()
	p.reveal()
	ctx.ConsumeAndRedraw()
	return ""
}

func (p *Pane) handlePending(ctx *components.EventContext, e xui.KeyEvent) bool {
	if !p.pendingG {
		return false
	}
	p.pendingG = false
	if e.Code != xui.KeyRune || e.Rune != 'g' {
		return false
	}
	p.line, p.col, p.scroll, p.xScroll = 0, 0, 0, 0
	ctx.ConsumeAndRedraw()
	return true
}

func (p *Pane) handleRune(ctx *components.EventContext, r rune) string {
	switch r {
	case 'j':
		p.moveLine(1)
	case 'k':
		p.moveLine(-1)
	case 'h':
		p.moveCol(-1)
	case 'l':
		p.moveCol(1)
	case 'g':
		p.pendingG = true
		ctx.ConsumeAndRedraw()
		return ""
	case 'G':
		p.line = max(len(p.lines)-1, 0)
	case '0':
		p.col = 0
	case '$':
		p.col = len(p.lineText())
	case 'v', 'V':
		p.toggleSelect()
	case 'a':
		return p.addRef()
	case '/':
		p.searchMode = true
		p.searchQuery = ""
		p.matches = nil
		p.searchFrom = p.pos()
		ctx.ConsumeAndRedraw()
		return ""
	case 'n':
		msg := p.moveMatch(1)
		ctx.ConsumeAndRedraw()
		return msg
	case 'N':
		msg := p.moveMatch(-1)
		ctx.ConsumeAndRedraw()
		return msg
	case ':':
		p.gotoMode = true
		p.gotoBuf = ""
		ctx.ConsumeAndRedraw()
		return ""
	case 'p':
		p.selectParagraph()
		ctx.ConsumeAndRedraw()
		return ""
	default:
		ctx.Consume = true
		return ""
	}
	p.clamp()
	p.reveal()
	ctx.ConsumeAndRedraw()
	return ""
}

func (p *Pane) notify(msg string) {
	if msg != "" {
		p.onToast(msg)
	}
}

// load reads path and moves the cursor to line (1-based, 0 = top).
func (p *Pane) load(path string, line int) error {
	abs := p.resolve(path)
	lines, err := readFileLines(abs)
	if err != nil {
		return err
	}

	p.abs = abs
	p.rel = relPath(p.cwd, abs)
	p.lines = lines
	p.lowered = nil // the next search builds its copy from these lines
	p.hl = codeview.Highlight(abs, lines, p.theme)
	p.loadErr = ""
	p.selecting = false
	if !p.active {
		return nil
	}
	p.line = clampLine(line-1, len(lines))
	// Land the caret on the code, not in the indentation: an open at :line
	// should show the line, not its leading whitespace.
	p.col = firstNonBlank(p.lineText())
	p.scroll = 0
	p.xScroll = 0
	p.clamp()
	return nil
}

func (p *Pane) resolve(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(p.cwd, path))
}

// readFileLines reads a text file into display lines. Sensitive paths, binary
// files and oversized files are refused rather than half-rendered.
func readFileLines(abs string) ([]string, error) {
	if permission.IsSensitivePath(abs, sensitivePaths) {
		return nil, fmt.Errorf("%s is a sensitive path", filepath.Base(abs))
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf("%s is a directory", abs)
	}
	if st.Size() > maxFileBytes {
		return nil, fmt.Errorf("file is %s; the viewer caps at %s", humanBytes(st.Size()), humanBytes(maxFileBytes))
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	head := raw
	if len(head) > binarySniffSize {
		head = head[:binarySniffSize]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return nil, fmt.Errorf("%s looks binary", filepath.Base(abs))
	}
	lines := strings.Split(util.NormalizeLF(string(raw)), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines, nil
}

// loadMessage renders a load failure the way the status row wants it: the path
// relative to cwd, not an absolute path the status row would truncate before
// the reason becomes legible. The reason itself is mapped to short, OS-neutral
// text — Windows syscall strings are long and hard to scan.
func loadMessage(cwd string, err error) string {
	if pe, ok := errors.AsType[*os.PathError](err); ok {
		return fmt.Sprintf("%s: %s", relPath(cwd, pe.Path), statReason(pe.Err))
	}
	return err.Error()
}

func statReason(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "no such file"
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	default:
		return err.Error()
	}
}

// relPath renders abs for the UI with forward slashes: titles, status rows and
// pasted references are shown on every platform, and "src\\a.go:2" is worthless
// in a terminal or editor on the other OS.
func relPath(cwd, abs string) string {
	rel, err := filepath.Rel(cwd, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func (p *Pane) lineText() string {
	if p.line < 0 || p.line >= len(p.lines) {
		return ""
	}
	return p.lines[p.line]
}

func (p *Pane) moveLine(delta int) {
	p.line += delta
	p.clamp()
	p.col = clampCol(p.lineText(), p.col)
	p.reveal()
}

func (p *Pane) moveCol(delta int) {
	s := p.lineText()
	if delta < 0 {
		if p.col > 0 {
			_, size := utf8.DecodeLastRuneInString(s[:p.col])
			p.col -= size
		}
	} else if p.col < len(s) {
		_, size := utf8.DecodeRuneInString(s[p.col:])
		p.col += size
	}
	p.reveal()
}

func (p *Pane) page() int {
	h := p.viewH / 2
	if h < 1 {
		h = 8
	}
	return h
}

func (p *Pane) clamp() {
	p.line = clampLine(p.line, len(p.lines))
	p.col = clampCol(p.lineText(), p.col)
}

// reveal scrolls so the cursor line and column are inside the viewport.
func (p *Pane) reveal() {
	if p.viewH > 0 {
		if p.line < p.scroll {
			p.scroll = p.line
		}
		if bottom := p.scroll + p.viewH - 1; p.line > bottom {
			p.scroll = p.line - p.viewH + 1
		}
	}
	if p.viewW > 0 {
		col := displayCol(p.lineText(), p.col, p.method)
		codeW := max(p.viewW-gutterWidth(len(p.lines)), 1)
		if col < p.xScroll {
			p.xScroll = col
		}
		if col >= p.xScroll+codeW {
			p.xScroll = col - codeW + 1
		}
	}
	if p.scroll < 0 {
		p.scroll = 0
	}
	if p.xScroll < 0 {
		p.xScroll = 0
	}
}

// caretPos is a cursor position, scroll included, that can be put back later.
type caretPos struct {
	line    int
	col     int
	scroll  int
	xScroll int
}

func (p *Pane) pos() caretPos {
	return caretPos{line: p.line, col: p.col, scroll: p.scroll, xScroll: p.xScroll}
}

// jumpTo restores a caretPos verbatim. reveal only nudges the scroll when the
// caret falls outside the viewport, so a canceled search would not get its
// scroll back without the explicit restore.
func (p *Pane) jumpTo(c caretPos) {
	p.line, p.col, p.scroll, p.xScroll = c.line, c.col, c.scroll, c.xScroll
	p.clamp()
	p.reveal()
}

// jumpToLine lands on the first non-blank of a line: a goto and a search hit
// both want the code, not the indentation in front of it.
func (p *Pane) jumpToLine(line int) {
	p.line = clampLine(line, len(p.lines))
	p.col = firstNonBlank(p.lineText())
	p.clamp()
	p.reveal()
}

func clampLine(line, total int) int {
	if total <= 0 {
		return 0
	}
	return min(max(line, 0), total-1)
}

// firstNonBlank is the column an open at :line lands on: the first character
// that is not indentation, so the caret sits on the code rather than in
// whitespace.
func firstNonBlank(s string) int {
	i := 0
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r != ' ' && r != '\t' {
			break
		}
		i += size
	}
	return i
}

func clampCol(s string, col int) int {
	if col <= 0 {
		return 0
	}
	if col > len(s) {
		return len(s)
	}
	for col < len(s) && !utf8.RuneStart(s[col]) {
		col--
	}
	return col
}

// displayCol is the screen column of a byte offset, counting wide glyphs.
func displayCol(s string, col int, method xui.WidthMethod) int {
	if col <= 0 {
		return 0
	}
	if col > len(s) {
		col = len(s)
	}
	return xui.StringWidth(s[:clampCol(s, col)], method)
}

func gutterWidth(lines int) int {
	return len(strconv.Itoa(max(lines, 1))) + 3
}

// toggleSelect starts or drops a line-wise selection. The caret is the moving
// end, so the ordinary movement keys extend it without any extra state.
func (p *Pane) toggleSelect() {
	p.selecting = !p.selecting
	p.selAnchor = p.line
}

// selectionLines returns the inclusive, ordered line span of the selection.
func (p *Pane) selectionLines() (lo, hi int) {
	lo, hi = p.selAnchor, p.line
	if lo > hi {
		lo, hi = hi, lo
	}
	return clampLine(lo, len(p.lines)), clampLine(hi, len(p.lines))
}

// addRef hands the selected lines (or the caret line alone) to the chat input
// and reports the outcome as a toast.
func (p *Pane) addRef() string {
	ref, ok := p.selectedRef()
	if !ok {
		return "no file open"
	}
	p.selecting = false
	p.onRef(ref)
	return fmt.Sprintf("added %s to chat", ref.Label())
}

// selectedRef cuts the current selection out of the open file.
func (p *Pane) selectedRef() (chat.Ref, bool) {
	if p.abs == "" || len(p.lines) == 0 {
		return chat.Ref{}, false
	}
	lo, hi := p.line, p.line
	if p.selecting {
		lo, hi = p.selectionLines()
	}
	text := strings.Join(p.lines[lo:hi+1], "\n")
	return chat.Ref{
		Path:  p.rel,
		Start: lo + 1,
		End:   hi + 1,
		Lang:  fenceLang(p.abs),
		Text:  text,
	}, true
}

// fenceLang is the markdown fence hint for a path: "main.go" -> "go".
func fenceLang(path string) string {
	return strings.TrimPrefix(filepath.Ext(path), ".")
}

// Draw paints the overlay.
func (p *Pane) Draw(ctx components.DrawContext) components.Surface {
	p.method = ctx.Method
	p.viewW = ctx.Max.Width
	p.viewH = max(ctx.Max.Height-2, 1)
	p.reveal()
	return codeview.Paint(ctx, p.snapshot(ctx))
}

func (p *Pane) snapshot(ctx components.DrawContext) codeview.Model {
	m := codeview.Model{
		Theme:      p.theme,
		Title:      "code" + chrome.Sep + p.title(),
		Status:     p.status(),
		Hint:       hintLine,
		Path:       p.abs,
		Lines:      p.lines,
		Highlight:  p.hl,
		CursorLine: p.line,
		CursorCol:  displayCol(p.lineText(), p.col, ctx.Method),
		Scroll:     p.scroll,
		XScroll:    p.xScroll,
		ViewH:      p.viewH,
		Empty:      p.emptyText(),
	}
	if p.selecting {
		lo, hi := p.selectionLines()
		m.Selecting = true
		// Line-wise: the moving end is pinned past any real line so the
		// tint is clipped at the frame edge rather than stopping mid-line.
		m.SelStart = components.Point{X: 0, Y: lo}
		m.SelEnd = components.Point{X: selLineWide, Y: hi}
	}
	return m
}

// status is the resting status row: where the reader is and where the search
// stands, or the prompt itself while one is being typed.
func (p *Pane) status() string {
	switch {
	case p.loadErr != "":
		return p.loadErr
	case p.searchMode:
		return p.searchPrompt()
	case p.gotoMode:
		return p.gotoPrompt()
	case p.selecting:
		lo, hi := p.selectionLines()
		return selectionStatus(hi - lo + 1)
	}
	loc, label := p.location(), p.matchLabel()
	if loc == "" || label == "" {
		return loc
	}
	// The query outlives the prompt — n and N repeat it — so the counter rides
	// beside the location instead of replacing it.
	return loc + chrome.Sep + label
}

// selectionStatus is the status row while v is active.
func selectionStatus(n int) string {
	unit := "lines"
	if n == 1 {
		unit = "line"
	}
	return fmt.Sprintf("%d %s selected%s a to add", n, unit, chrome.Sep)
}

func (p *Pane) title() string {
	if p.rel == "" {
		return "no file"
	}
	return p.rel
}

// location is the resting status text: path, caret, line count.
func (p *Pane) location() string {
	if p.abs == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d:%d%s%d lines", p.rel, p.line+1, p.col+1, chrome.Sep, len(p.lines))
}

func (p *Pane) emptyText() string {
	if p.loadErr != "" {
		return p.loadErr
	}
	if p.abs == "" {
		return "usage: /code <path>"
	}
	if len(p.lines) == 0 {
		return "empty file"
	}
	return ""
}

// hintLine is advertised at the bottom of the pane.
var hintLine = strings.Join([]string{
	"esc close",
	"j/k move",
	"/ find",
	": line",
	"v select",
	"p para",
	"a add",
}, chrome.Sep)

// updateSearch rebuilds the match list for the query typed so far and lands the
// caret on the first match at or below the line the prompt opened on, wrapping
// to the top when the query only matches above it. Anchoring on that opening
// line, not on the caret, is what keeps the caret from walking down the file as
// characters are added to the query.
func (p *Pane) updateSearch() {
	q := strings.ToLower(p.searchQuery)
	p.matches = p.matches[:0]
	if q == "" {
		return
	}
	p.ensureLowered()
	for i, line := range p.lowered {
		if strings.Contains(line, q) {
			p.matches = append(p.matches, i)
		}
	}
	if len(p.matches) > 0 {
		p.jumpToLine(p.matches[p.firstMatchAtOrAfter(p.searchFrom.line)])
	}
}

// ensureLowered builds the lowercase copy search scans. One pass per file beats
// a ToLower per line per keystroke: on an 8 MiB file the first character typed
// pays ~9 ms once, and the ones after it scan in ~1 ms.
func (p *Pane) ensureLowered() {
	if p.lowered != nil {
		return
	}
	p.lowered = make([]string, len(p.lines))
	for i, line := range p.lines {
		p.lowered[i] = strings.ToLower(line)
	}
}

// moveMatch steps n (dir > 0) and N (dir < 0) from the caret. Measuring from
// the caret rather than from the index of the match last visited is what stops
// n from stepping backwards after a j or k.
func (p *Pane) moveMatch(dir int) string {
	if len(p.matches) == 0 {
		if p.searchQuery == "" {
			return "" // no query to repeat: n has nothing to do
		}
		p.updateSearch()
		if len(p.matches) == 0 {
			return "no matches"
		}
	}
	idx := p.matchBefore(p.line)
	if dir > 0 {
		idx = p.matchAfter(p.line)
	}
	p.jumpToLine(p.matches[idx])
	return ""
}

// searchPrompt is the status row while a query is being typed: the query, then
// the caret's place among its matches.
func (p *Pane) searchPrompt() string {
	out := "/" + p.searchQuery
	if label := p.matchLabel(); label != "" {
		out += "  " + label
	}
	return out
}

// gotoPrompt is the status row while : takes a line number: the digits typed
// and the file's length, so the range a number has to land in is on screen.
func (p *Pane) gotoPrompt() string {
	if p.gotoBuf == "" {
		return ":"
	}
	return fmt.Sprintf(":%s%s%d lines", p.gotoBuf, chrome.Sep, len(p.lines))
}

// matchLabel is the caret's place among the matches: "2/5" while it sits on
// one, "no matches" for a query that hit nothing, "" when no query is live. It
// is derived from the caret line, so a j/k after a search cannot leave a stale
// counter on screen.
func (p *Pane) matchLabel() string {
	if p.searchQuery == "" {
		return ""
	}
	if len(p.matches) == 0 {
		return "no matches"
	}
	if i := p.matchIndex(); i >= 0 {
		return fmt.Sprintf("%d/%d", i+1, len(p.matches))
	}
	return ""
}

// matchIndex is where the caret sits in the match list, or -1 when its line is
// not one of them.
func (p *Pane) matchIndex() int {
	for i, m := range p.matches {
		if m == p.line {
			return i
		}
	}
	return -1
}

// firstMatchAtOrAfter is the index of the first match on or below line, or the
// top of the file when the query only matches above it.
func (p *Pane) firstMatchAtOrAfter(line int) int {
	for i, m := range p.matches {
		if m >= line {
			return i
		}
	}
	return 0
}

// matchAfter is the index of the first match below line, wrapping to the top:
// what n does at the end of the file.
func (p *Pane) matchAfter(line int) int {
	for i, m := range p.matches {
		if m > line {
			return i
		}
	}
	return 0
}

// matchBefore is the index of the last match above line, wrapping to the
// bottom: what N does at the top of the file.
func (p *Pane) matchBefore(line int) int {
	idx := len(p.matches) - 1 // nothing above: wrap to the bottom match
	for i, m := range p.matches {
		if m >= line {
			break
		}
		idx = i
	}
	return idx
}

// selectParagraph selects the paragraph around the caret: the run of non-blank
// lines it sits in. The caret parks on the paragraph's last line, as it must,
// because the selection has one moving end and a paragraph cannot be selected
// without the caret riding it.
func (p *Pane) selectParagraph() {
	if len(p.lines) == 0 {
		return
	}
	lo, hi := p.paragraph()
	if p.selecting && p.selAnchor == lo && p.line == hi {
		p.selecting = false // p again drops the paragraph it just selected
		return
	}
	p.selecting = true
	p.selAnchor = lo
	p.jumpToLine(hi)
}

// paragraph is the run of non-blank lines the caret sits in.
func (p *Pane) paragraph() (lo, hi int) {
	lo, hi = p.line, p.line
	for lo > 0 && strings.TrimSpace(p.lines[lo-1]) != "" {
		lo--
	}
	for hi < len(p.lines)-1 && strings.TrimSpace(p.lines[hi+1]) != "" {
		hi++
	}
	return lo, hi
}
