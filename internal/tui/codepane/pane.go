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

	// search
	searchMode  bool
	searchQuery string
	matches     []int
	matchIdx    int

	// goto line
	gotoMode bool
	gotoBuf  string

	// word selection on a single line
	selWord string
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
	p.selWord = ""
	p.searchMode = false
	p.searchQuery = ""
	p.matches = nil
	p.matchIdx = 0
	p.gotoMode = false
	p.gotoBuf = ""

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
	p.searchMode = false
	p.gotoMode = false
	p.selWord = ""
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
		p.searchMode = false
		p.searchQuery = ""
		p.matches = nil
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
		if p.gotoBuf != "" {
			if n, err := strconv.Atoi(p.gotoBuf); err == nil && n > 0 {
				p.line = clampLine(n-1, len(p.lines))
				p.col = firstNonBlank(p.lineText())
				p.clamp()
				p.reveal()
			}
		}
		p.gotoBuf = ""
	case xui.KeyBackspace:
		if len(p.gotoBuf) > 0 {
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
		p.selWord = ""
		ctx.ConsumeAndRedraw()
		return ""
	}
	if e.Code == xui.KeyEscape ||
		(e.Code == xui.KeyRune && (e.Rune == 'q' || e.Rune == 'Q') && !e.Mods.Has(xui.ModCtrl)) {
		p.active = false
		p.selecting = false // a reopened pane starts clean
		p.selWord = ""
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
		p.selWord = ""
		p.moveLine(1)
	case 'k':
		p.selWord = ""
		p.moveLine(-1)
	case 'h':
		p.selWord = ""
		p.moveCol(-1)
	case 'l':
		p.selWord = ""
		p.moveCol(1)
	case 'g':
		p.pendingG = true
		ctx.ConsumeAndRedraw()
		return ""
	case 'G':
		p.selWord = ""
		p.line = max(len(p.lines)-1, 0)
	case '0':
		p.selWord = ""
		p.col = 0
	case '$':
		p.selWord = ""
		p.col = len(p.lineText())
	case 'v', 'V':
		p.toggleSelect()
	case 'a':
		return p.addRef()
	case '/':
		p.searchMode = true
		p.searchQuery = ""
		p.matches = nil
		p.matchIdx = 0
		ctx.ConsumeAndRedraw()
		return ""
	case 'n':
		p.selWord = ""
		msg := p.moveMatch(1)
		ctx.ConsumeAndRedraw()
		return msg
	case 'N':
		p.selWord = ""
		msg := p.moveMatch(-1)
		ctx.ConsumeAndRedraw()
		return msg
	case ':':
		p.gotoMode = true
		p.gotoBuf = ""
		ctx.ConsumeAndRedraw()
		return ""
	case '{':
		p.selWord = ""
		p.jumpParagraph(-1)
	case '}':
		p.selWord = ""
		p.jumpParagraph(1)
	case '%':
		p.selWord = ""
		p.jumpMatchingBracket()
	case 'w':
		p.selWord = ""
		p.moveWord(1)
	case 'b':
		p.selWord = ""
		p.moveWord(-1)
	case 'e':
		p.selWord = ""
		p.moveWordEnd()
	case 'B':
		p.selectEnclosingBlock()
		ctx.ConsumeAndRedraw()
		return ""
	case 'W':
		p.selectWordUnderCursor()
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
	p.selWord = ""
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
	p.selWord = ""
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
	if p.selecting && p.selWord != "" && lo == hi {
		text = p.selWord
	}
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
	status := p.location()
	switch {
	case p.loadErr != "":
		status = p.loadErr
	case p.searchMode:
		status = p.searchStatus()
	case p.gotoMode:
		status = ":" + p.gotoBuf
	case p.selecting:
		if p.selWord != "" {
			status = fmt.Sprintf("'%s' selected%s a to add", p.selWord, chrome.Sep)
		} else {
			lo, hi := p.selectionLines()
			status = selectionStatus(hi - lo + 1)
		}
	case p.searchQuery != "":
		status = p.searchStatus()
	}
	m := codeview.Model{
		Theme:      p.theme,
		Title:      "code" + chrome.Sep + p.title(),
		Status:     status,
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
		if p.selWord != "" && lo == hi {
			sCol := displayCol(p.lineText(), p.col, ctx.Method)
			endByte := p.col + len(p.selWord)
			eCol := displayCol(p.lineText(), endByte, ctx.Method)
			if eCol > 0 {
				eCol--
			}
			if eCol < sCol {
				eCol = sCol
			}
			m.SelStart = components.Point{X: sCol, Y: lo}
			m.SelEnd = components.Point{X: eCol, Y: hi}
		} else {
			// Line-wise: the moving end is pinned past any real line so the
			// tint is clipped at the frame edge rather than stopping mid-line.
			m.SelStart = components.Point{X: 0, Y: lo}
			m.SelEnd = components.Point{X: selLineWide, Y: hi}
		}
	}
	return m
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
	"B block",
	"% match",
	"a add",
}, chrome.Sep)

func (p *Pane) updateSearch() {
	q := strings.ToLower(p.searchQuery)
	p.matches = p.matches[:0]
	if q == "" {
		return
	}
	for i, line := range p.lines {
		if strings.Contains(strings.ToLower(line), q) {
			p.matches = append(p.matches, i)
		}
	}
	if len(p.matches) > 0 {
		p.matchIdx = 0
		for i, m := range p.matches {
			if m >= p.line {
				p.matchIdx = i
				break
			}
		}
		p.line = p.matches[p.matchIdx]
		p.col = firstNonBlank(p.lineText())
		p.clamp()
		p.reveal()
	}
}

func (p *Pane) moveMatch(dir int) string {
	if len(p.matches) == 0 {
		if p.searchQuery != "" {
			p.updateSearch()
		}
		if len(p.matches) == 0 {
			return "no matches"
		}
	}
	p.matchIdx = (p.matchIdx + dir) % len(p.matches)
	if p.matchIdx < 0 {
		p.matchIdx = len(p.matches) - 1
	}
	p.line = p.matches[p.matchIdx]
	p.col = firstNonBlank(p.lineText())
	p.clamp()
	p.reveal()
	return ""
}

func (p *Pane) searchStatus() string {
	if p.searchQuery == "" {
		return "/"
	}
	out := "/" + p.searchQuery
	if len(p.matches) == 0 {
		return out + "  no matches"
	}
	return fmt.Sprintf("%s  %d/%d", out, p.matchIdx+1, len(p.matches))
}

func (p *Pane) jumpParagraph(dir int) {
	if len(p.lines) == 0 {
		return
	}
	line := p.line
	if dir > 0 {
		for line < len(p.lines)-1 && strings.TrimSpace(p.lines[line]) == "" {
			line++
		}
		for line < len(p.lines)-1 && strings.TrimSpace(p.lines[line]) != "" {
			line++
		}
	} else {
		for line > 0 && strings.TrimSpace(p.lines[line]) == "" {
			line--
		}
		for line > 0 && strings.TrimSpace(p.lines[line]) != "" {
			line--
		}
	}
	p.line = line
	p.col = firstNonBlank(p.lineText())
}

func (p *Pane) jumpMatchingBracket() {
	if len(p.lines) == 0 || p.line >= len(p.lines) {
		return
	}
	lineText := p.lineText()
	openBrackets := "({["
	pairs := map[rune]rune{
		'(': ')',
		'{': '}',
		'[': ']',
		')': '(',
		'}': '{',
		']': '[',
	}

	targetRune := rune(0)
	targetCol := p.col
	if p.col < len(lineText) {
		r, _ := utf8.DecodeRuneInString(lineText[p.col:])
		if _, ok := pairs[r]; ok {
			targetRune = r
		}
	}
	if targetRune == 0 {
		for i, r := range lineText {
			if i >= p.col {
				if _, ok := pairs[r]; ok {
					targetRune = r
					targetCol = i
					break
				}
			}
		}
	}
	if targetRune == 0 {
		for i, r := range lineText {
			if _, ok := pairs[r]; ok {
				targetRune = r
				targetCol = i
				break
			}
		}
	}
	if targetRune == 0 {
		return
	}

	matchRune := pairs[targetRune]
	isOpen := strings.ContainsRune(openBrackets, targetRune)

	depth := 0
	if isOpen {
		for l := p.line; l < len(p.lines); l++ {
			txt := p.lines[l]
			startCol := 0
			if l == p.line {
				startCol = targetCol
			}
			for col := startCol; col < len(txt); {
				r, size := utf8.DecodeRuneInString(txt[col:])
				if r == targetRune {
					depth++
				} else if r == matchRune {
					depth--
					if depth == 0 {
						p.line = l
						p.col = col
						return
					}
				}
				col += size
			}
		}
	} else {
		for l := p.line; l >= 0; l-- {
			txt := p.lines[l]
			endCol := len(txt)
			if l == p.line {
				endCol = targetCol + 1
			}
			type colRune struct {
				col int
				r   rune
			}
			var runes []colRune
			for col := 0; col < endCol; {
				r, size := utf8.DecodeRuneInString(txt[col:])
				runes = append(runes, colRune{col: col, r: r})
				col += size
			}
			for i := len(runes) - 1; i >= 0; i-- {
				cr := runes[i]
				if cr.r == targetRune {
					depth++
				} else if cr.r == matchRune {
					depth--
					if depth == 0 {
						p.line = l
						p.col = cr.col
						return
					}
				}
			}
		}
	}
}

func isWordChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
}

func (p *Pane) moveWord(dir int) {
	if len(p.lines) == 0 {
		return
	}
	if dir > 0 {
		l := p.line
		txt := p.lineText()
		c := p.col

		if c >= len(txt) {
			if l < len(p.lines)-1 {
				p.line++
				p.col = firstNonBlank(p.lineText())
			}
			return
		}

		r, _ := utf8.DecodeRuneInString(txt[c:])
		inWord := isWordChar(r)
		isSpace := r == ' ' || r == '\t'

		if inWord {
			for c < len(txt) {
				r2, s2 := utf8.DecodeRuneInString(txt[c:])
				if !isWordChar(r2) {
					break
				}
				c += s2
			}
		} else if !isSpace {
			for c < len(txt) {
				r2, s2 := utf8.DecodeRuneInString(txt[c:])
				if isWordChar(r2) || r2 == ' ' || r2 == '\t' {
					break
				}
				c += s2
			}
		}
		for c < len(txt) {
			r2, s2 := utf8.DecodeRuneInString(txt[c:])
			if r2 != ' ' && r2 != '\t' {
				break
			}
			c += s2
		}
		if c >= len(txt) && l < len(p.lines)-1 {
			p.line++
			p.col = firstNonBlank(p.lineText())
		} else {
			p.col = c
		}
	} else {
		l := p.line
		txt := p.lineText()
		c := p.col

		if c <= 0 {
			if l > 0 {
				p.line--
				p.col = len(p.lineText())
				p.moveWord(-1)
			}
			return
		}

		_, prevSize := utf8.DecodeLastRuneInString(txt[:c])
		c -= prevSize

		for c > 0 {
			r, _ := utf8.DecodeRuneInString(txt[c:])
			if r != ' ' && r != '\t' {
				break
			}
			_, s := utf8.DecodeLastRuneInString(txt[:c])
			c -= s
		}

		r, _ := utf8.DecodeRuneInString(txt[c:])
		inWord := isWordChar(r)
		for c > 0 {
			_, prevS := utf8.DecodeLastRuneInString(txt[:c])
			prevR, _ := utf8.DecodeRuneInString(txt[c-prevS:])
			if inWord && !isWordChar(prevR) {
				break
			}
			if !inWord && (isWordChar(prevR) || prevR == ' ' || prevR == '\t') {
				break
			}
			c -= prevS
		}
		p.col = c
	}
}

func (p *Pane) moveWordEnd() {
	if len(p.lines) == 0 {
		return
	}
	txt := p.lineText()
	c := p.col
	if c >= len(txt) {
		if p.line < len(p.lines)-1 {
			p.line++
			p.col = 0
			p.moveWordEnd()
		}
		return
	}

	_, size := utf8.DecodeRuneInString(txt[c:])
	c += size

	for c < len(txt) {
		r, s := utf8.DecodeRuneInString(txt[c:])
		if r != ' ' && r != '\t' {
			break
		}
		c += s
	}
	if c >= len(txt) {
		p.col = len(txt)
		return
	}

	r, _ := utf8.DecodeRuneInString(txt[c:])
	inWord := isWordChar(r)
	for c < len(txt) {
		nextCol := c
		r2, s2 := utf8.DecodeRuneInString(txt[nextCol:])
		if inWord && !isWordChar(r2) {
			break
		}
		if !inWord && (isWordChar(r2) || r2 == ' ' || r2 == '\t') {
			break
		}
		c += s2
	}
	if c > 0 {
		_, lastS := utf8.DecodeLastRuneInString(txt[:c])
		c -= lastS
	}
	p.col = c
}

func (p *Pane) selectEnclosingBlock() {
	if len(p.lines) == 0 {
		return
	}
	startLine, endLine := p.line, p.line
	if p.selecting {
		startLine, endLine = p.selectionLines()
	}

	for l := startLine; l >= 0; l-- {
		txt := p.lines[l]
		for col := 0; col < len(txt); {
			r, size := utf8.DecodeRuneInString(txt[col:])
			if r == '{' {
				depth := 1
				matchL := -1
				for ml := l; ml < len(p.lines); ml++ {
					mtxt := p.lines[ml]
					mStart := 0
					if ml == l {
						mStart = col + size
					}
					for mc := mStart; mc < len(mtxt); {
						mr, msize := utf8.DecodeRuneInString(mtxt[mc:])
						if mr == '{' {
							depth++
						} else if mr == '}' {
							depth--
							if depth == 0 {
								matchL = ml
								break
							}
						}
						mc += msize
					}
					if depth == 0 {
						break
					}
				}
				if matchL >= 0 && (matchL > endLine || (matchL >= endLine && l < startLine) || (matchL >= endLine && !p.selecting)) {
					openLine := l
					for openLine > 0 && strings.TrimSpace(p.lines[openLine-1]) != "" &&
						!strings.ContainsRune(p.lines[openLine-1], '}') &&
						!strings.ContainsRune(p.lines[openLine-1], '{') {
						openLine--
					}
					p.selecting = true
					p.selAnchor = openLine
					p.line = matchL
					p.selWord = ""
					p.clamp()
					p.reveal()
					return
				}
			}
			col += size
		}
	}

	p.selectParagraph()
}

func (p *Pane) selectParagraph() {
	if len(p.lines) == 0 {
		return
	}
	lo := p.line
	hi := p.line
	for lo > 0 && strings.TrimSpace(p.lines[lo-1]) != "" {
		lo--
	}
	for hi < len(p.lines)-1 && strings.TrimSpace(p.lines[hi+1]) != "" {
		hi++
	}
	p.selecting = true
	p.selAnchor = lo
	p.line = hi
	p.selWord = ""
	p.clamp()
	p.reveal()
}

func (p *Pane) selectWordUnderCursor() {
	if len(p.lines) == 0 || p.line >= len(p.lines) {
		return
	}
	txt := p.lineText()
	if len(txt) == 0 {
		return
	}
	col := clampCol(txt, p.col)
	if col >= len(txt) {
		col = max(0, len(txt)-1)
	}

	r, size := utf8.DecodeRuneInString(txt[col:])
	inWord := isWordChar(r)
	if !inWord && (r == ' ' || r == '\t') {
		found := false
		for c := col; c < len(txt); {
			r2, s2 := utf8.DecodeRuneInString(txt[c:])
			if isWordChar(r2) {
				col = c
				r = r2
				size = s2
				inWord = true
				found = true
				break
			}
			c += s2
		}
		if !found {
			return
		}
	}

	start := col
	end := col + size
	for start > 0 {
		_, prevS := utf8.DecodeLastRuneInString(txt[:start])
		prevR, _ := utf8.DecodeRuneInString(txt[start-prevS:])
		if inWord && !isWordChar(prevR) {
			break
		}
		if !inWord && (isWordChar(prevR) || prevR == ' ' || prevR == '\t') {
			break
		}
		start -= prevS
	}
	for end < len(txt) {
		nextR, nextS := utf8.DecodeRuneInString(txt[end:])
		if inWord && !isWordChar(nextR) {
			break
		}
		if !inWord && (isWordChar(nextR) || nextR == ' ' || nextR == '\t') {
			break
		}
		end += nextS
	}

	word := txt[start:end]
	if word == "" {
		return
	}

	p.selecting = true
	p.selAnchor = p.line
	p.col = start
	p.selWord = word
	p.clamp()
	p.reveal()
}
