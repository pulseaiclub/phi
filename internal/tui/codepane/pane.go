// Package codepane renders a full-screen source-file pane: syntax-highlighted
// reading plus language-server navigation (definition, references, hover,
// document outline).
//
// Language servers are optional. Without one the pane stays a viewer; every
// question falls back to an honest error instead of a guess.
package codepane

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/chat"
	"github.com/pulseaiclub/phi/internal/components/chrome"
	"github.com/pulseaiclub/phi/internal/components/codeview"
	"github.com/pulseaiclub/phi/internal/components/layout"
	"github.com/pulseaiclub/phi/internal/components/listpicker"
	"github.com/pulseaiclub/phi/internal/components/text"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/util"
	"github.com/pulseaiclub/phi/internal/util/lsp"
)

const (
	// lspCallTimeout bounds one server round-trip. A cold gopls indexing a large
	// module can exceed it, but the pane stays usable and the call is cancellable.
	lspCallTimeout = 20 * time.Second
	// maxFileBytes keeps a stray multi-gigabyte log from freezing the reader.
	maxFileBytes = 8 << 20
	// binarySniffSize: a NUL byte in the head is the cheapest binary tell.
	binarySniffSize = 8 << 10

	cardZ      = 20
	cardMaxRow = 18
	cardMinW   = 24
	// scrollStep is how far h/l jump horizontally.
	scrollStep = 4
	// selLineWide marks the moving end of a line-wise selection. It only has to
	// exceed any real line, so the tint is clipped at the frame edge instead of
	// stopping mid-line.
	selLineWide = 1 << 20
)

// sensitivePaths is a snapshot of the gate's deny list. The viewer reads files
// itself, so it checks the same list rather than becoming the one hole in it.
var sensitivePaths = permission.SensitivePaths()

// Pane is a full-screen source-file overlay.
//
// Language-server queries run off the UI goroutine, so every field is guarded
// by mu. Handle and Draw take it briefly; a query completes by taking it once
// to land its result and then asking for a repaint.
type Pane struct {
	cwd     string
	mgr     *lsp.Manager
	onRef   func(chat.Ref)
	onCopy  func(string) bool
	onToast func(string)
	onWake  func()

	mu       sync.Mutex
	theme    components.Theme
	themeVer int // bumped by SetTheme so a slow highlight can notice
	method   xui.WidthMethod

	active  bool
	abs     string
	rel     string
	lines   []string
	hl      map[int][]components.Span
	loadErr string
	status  string
	busy    bool

	// req counts file loads; a query goroutine captures it and drops its
	// answer when the pane has moved on, so a slow server cannot overwrite the
	// file the user is reading now.
	req         uint64
	pendingCopy string
	pendingWake bool // set under mu; Handle calls wake() after unlocking
	// pendingRef is staged by the key handler and delivered by Handle, because a
	// callback must not run while mu is held.
	pendingRef    *chat.Ref
	pendingReload bool

	line    int // 0-based cursor line
	col     int // byte offset into lines[line], always on a rune boundary
	scroll  int
	xScroll int
	viewW   int
	viewH   int

	pendingG bool
	help     bool

	// selecting is a line-wise visual selection anchored at selAnchor; the caret
	// (line) is the moving end, so plain movement extends it.
	selecting bool
	selAnchor int

	card *hoverCard

	// navKind/navHits hold the last answer; pushPicker tells Draw (the UI
	// goroutine) to open the result picker, since a query goroutine must not
	// touch the picker's widget state.
	navKind    string
	navHits    []lsp.Hit
	outline    []lsp.Symbol
	pushPicker bool

	searchMode  bool
	searchQuery string
	matches     []int
	matchIdx    int

	// picker is UI-goroutine-only state; the mutex above is still held around
	// it because Handle and Draw already run under it.
	picker listpicker.Picker
}

// hoverCard is one hover answer waiting to be painted.
type hoverCard struct {
	title     string
	signature string
	doc       string
}

// New builds an inactive pane. mgr may be nil, in which case language-server
// actions report that they are unavailable. onRef receives the line the user
// picked for the chat input. onWake requests a repaint from any goroutine and
// must be safe to call off the UI thread; both callbacks run outside the pane
// mutex, on the UI goroutine.
func New(
	theme components.Theme,
	cwd string,
	mgr *lsp.Manager,
	onRef func(chat.Ref),
	onCopy func(string) bool,
	onToast func(string),
	onWake func(),
) *Pane {
	p := &Pane{
		cwd:     cwd,
		mgr:     mgr,
		onRef:   onRef,
		onCopy:  onCopy,
		onToast: onToast,
		onWake:  onWake,
		theme:   theme,
		method:  xui.WidthUnicode,
		picker:  listpicker.Picker{Theme: theme},
	}
	p.picker.OnAccept = p.acceptPicker
	return p
}

// Active reports whether the overlay is showing.
func (p *Pane) Active() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.active
}

// SetTheme updates chrome and syntax colors.
func (p *Pane) SetTheme(th components.Theme) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.theme = th
	p.themeVer++
	p.picker.Theme = th
	p.hl = codeview.Highlight(p.abs, p.lines, th)
}

// Open shows a file from the top. Relative paths resolve against the pane cwd.
func (p *Pane) Open(path string) {
	p.OpenAt(path, 0)
}

// OpenAt shows path with the cursor on line (1-based; 0 leaves it at the top).
func (p *Pane) OpenAt(path string, line int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.active = true
	p.help = false
	p.searchMode = false
	p.searchQuery = ""
	p.matches = nil
	p.pendingG = false
	p.card = nil
	p.status = ""
	p.selecting = false
	p.navHits = nil
	p.outline = nil
	p.pushPicker = false
	p.busy = false
	p.req++ // any answer already in flight belongs to the previous file
	p.picker.Hide()
	p.mu.Unlock()

	if err := p.load(path, line); err != nil {
		p.mu.Lock()
		p.lines = nil
		p.hl = nil
		p.abs, p.rel = "", ""
		p.matches = nil
		p.loadErr = loadMessage(p.cwd, err)
		p.line, p.col, p.scroll, p.xScroll = 0, 0, 0, 0
		p.mu.Unlock()
	}
}

// Close hides the overlay.
func (p *Pane) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active = false
	p.req++ // a query answered after this must not reopen or repaint anything
	p.help = false
	p.searchMode = false
	p.card = nil
	p.pendingG = false
	p.picker.Hide()
}

// Handle consumes keyboard input while the overlay is active.
func (p *Pane) Handle(ctx *components.EventContext, ev xui.Event) {
	if p == nil || !p.Active() {
		return
	}
	switch e := ev.(type) {
	case xui.KeyEvent:
		if !e.Press {
			return
		}
		if p.pickerOpen() {
			p.picker.Handle(ctx, e)
			return
		}
		p.mu.Lock()
		msg := p.handleKeyLocked(ctx, e)
		text := p.pendingCopy
		p.pendingCopy = ""
		wake := p.pendingWake
		p.pendingWake = false
		ref := p.pendingRef
		p.pendingRef = nil
		// A reload must run here, not in the key handler: load takes the mutex
		// the handler already holds.
		reload := p.pendingReload
		p.pendingReload = false
		p.mu.Unlock()
		if text != "" {
			msg = p.copy(text)
		}
		if reload {
			msg = p.reload()
		}
		if ref != nil && p.onRef != nil {
			p.onRef(*ref)
		}
		if wake {
			p.wake()
		}
		p.notify(msg)
	default:
		ctx.Consume = true
	}
}

// handleKeyLocked runs one key against pane state. It returns a toast to raise
// after the lock is dropped, because callbacks must not run under the mutex.
func (p *Pane) handleKeyLocked(ctx *components.EventContext, e xui.KeyEvent) string {
	if p.searchMode {
		p.handleSearchKey(ctx, e)
		return ""
	}
	if p.help {
		p.help = false
		ctx.ConsumeAndRedraw()
		if e.Code == xui.KeyEscape || (e.Code == xui.KeyRune && e.Rune == '?') {
			return ""
		}
	}
	if e.Code == xui.KeyEscape && p.selecting {
		// Cancel the selection first: losing a ten-line pick to a close that was
		// meant to undo it is worse than one extra Esc.
		p.selecting = false
		ctx.ConsumeAndRedraw()
		return ""
	}
	if e.Code == xui.KeyEscape && p.card != nil {
		p.card = nil
		ctx.ConsumeAndRedraw()
		return ""
	}
	if e.Code == xui.KeyEscape ||
		(e.Code == xui.KeyRune && (e.Rune == 'q' || e.Rune == 'Q') && !e.Mods.Has(xui.ModCtrl)) {
		p.active = false
		p.selecting = false // a reopened pane starts clean
		ctx.ConsumeAndRedraw()
		return ""
	}
	if handled, msg := p.handlePending(ctx, e); handled {
		return msg
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
	case xui.KeyEnter:
		return p.definition()
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

func (p *Pane) handlePending(ctx *components.EventContext, e xui.KeyEvent) (bool, string) {
	if !p.pendingG {
		return false, ""
	}
	p.pendingG = false
	if e.Code != xui.KeyRune {
		return false, ""
	}
	msg := ""
	switch e.Rune {
	case 'g':
		p.line, p.col, p.scroll, p.xScroll = 0, 0, 0, 0
	case 'd':
		msg = p.definition()
	case 'r':
		msg = p.references()
	default:
		return false, ""
	}
	ctx.ConsumeAndRedraw()
	return true, msg
}

func (p *Pane) handleRune(ctx *components.EventContext, r rune) string {
	switch r {
	case 'j':
		p.moveLine(1)
	case 'k':
		p.moveLine(-1)
	case 'h':
		// Scrolling is not the caret moving, so leave the offset the user asked
		// for alone instead of letting reveal() snap it back.
		p.xScroll = max(0, p.xScroll-scrollStep)
		ctx.ConsumeAndRedraw()
		return ""
	case 'l':
		p.xScroll += scrollStep
		ctx.ConsumeAndRedraw()
		return ""
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
	case 'K':
		return p.hover()
	case 'o':
		return p.outlineSymbols()
	case 'y':
		return p.copyLine()
	case 'Y':
		return p.copyLocation()
	case 'v':
		p.toggleSelect()
	case 'a':
		return p.addRef()
	case 'r':
		p.pendingReload = true
		ctx.ConsumeAndRedraw()
		return ""
	case '/':
		p.searchMode = true
		p.searchQuery = ""
		p.matches = nil
		ctx.ConsumeAndRedraw()
		return ""
	case 'n':
		return p.moveMatch(1)
	case 'N':
		return p.moveMatch(-1)
	case '?':
		p.help = true
	default:
		ctx.Consume = true
		return ""
	}
	p.clamp()
	p.reveal()
	ctx.ConsumeAndRedraw()
	return ""
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

// ------------------------------------------------------------ LSP actions

// lspQuery is one in-flight question plus the file load it belongs to. An
// answer whose generation no longer matches is about a file the user has left.
type lspQuery struct {
	abs, rel  string
	line, col int
	id        uint64
}

// beginQuery snapshots the caret and takes the single-flight slot. ok is false
// when there is nothing to ask (no manager, no file, or a query already out).
func (p *Pane) beginQuery(kind string) (lspQuery, bool, string) {
	if p.busy {
		return lspQuery{}, false, ""
	}
	abs, rel, line, col, why := p.position()
	if why != "" {
		return lspQuery{}, false, why
	}
	p.busy = true
	p.status = kind + "…"
	return lspQuery{abs: abs, rel: rel, line: line, col: col, id: p.req}, true, ""
}

func (p *Pane) definition() string {
	q, ok, why := p.beginQuery("definition")
	if !ok {
		return why
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), lspCallTimeout)
		defer cancel()
		hits, err := p.mgr.Definition(ctx, q.abs, q.rel, q.line, q.col)
		p.land(q, "definition", hits, err)
	}()
	return ""
}

func (p *Pane) references() string {
	q, ok, why := p.beginQuery("references")
	if !ok {
		return why
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), lspCallTimeout)
		defer cancel()
		hits, err := p.mgr.References(ctx, q.abs, q.rel, q.line, q.col)
		p.land(q, "references", hits, err)
	}()
	return ""
}

func (p *Pane) hover() string {
	q, ok, why := p.beginQuery("hover")
	if !ok {
		return why
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), lspCallTimeout)
		defer cancel()
		info, err := p.mgr.Hover(ctx, q.abs, q.rel, q.line, q.col)
		p.mu.Lock()
		defer p.mu.Unlock()
		if q.id != p.req {
			p.busy = false
			return
		}
		p.busy = false
		switch {
		case err != nil:
			p.status = "hover: " + err.Error()
		case info == nil || info.Empty:
			p.status = "hover: nothing here"
		default:
			p.status = ""
			p.card = &hoverCard{title: fmt.Sprintf("%s:%d", q.rel, q.line), signature: info.Signature, doc: info.Doc}
		}
	}()
	p.wakeAsync(q.id)
	return ""
}

func (p *Pane) outlineSymbols() string {
	q, ok, why := p.beginQuery("outline")
	if !ok {
		return why
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), lspCallTimeout)
		defer cancel()
		syms, err := p.mgr.Symbols(ctx, q.abs, q.rel)
		p.mu.Lock()
		defer p.mu.Unlock()
		if q.id != p.req {
			p.busy = false
			return
		}
		p.busy = false
		switch {
		case err != nil:
			p.status = "outline: " + err.Error()
		case len(syms) == 0:
			p.status = "outline: no symbols"
		default:
			p.outline = syms
			p.status = fmt.Sprintf("%d symbols", len(syms))
			p.pushPicker = true
		}
	}()
	p.wakeAsync(q.id)
	return ""
}

// wakeAsync stages a repaint for a query goroutine: Handle runs wake() after
// unlocking. It must not take p.mu itself — it is called from handleKeyLocked,
// which already holds the lock, and Go's mutex is not reentrant.
func (p *Pane) wakeAsync(id uint64) {
	if p.req == id {
		p.pendingWake = true
	}
}

// land stores a navigation answer. A single hit jumps straight there; several
// hits queue the picker for the next Draw.
func (p *Pane) land(q lspQuery, kind string, hits []lsp.Hit, err error) {
	p.mu.Lock()
	p.busy = false
	if q.id != p.req {
		// The user moved on while the server was thinking: this answer is
		// about a file that is no longer on screen, and acting on it would
		// yank them somewhere they did not ask to be.
		p.mu.Unlock()
		return
	}
	if err != nil {
		p.status = kind + ": " + err.Error()
		p.mu.Unlock()
		p.wake()
		return
	}
	if len(hits) == 0 {
		p.status = kind + ": no results"
		p.mu.Unlock()
		p.wake()
		return
	}
	p.navKind = kind
	p.navHits = hits
	p.outline = nil
	p.status = fmt.Sprintf("%d %s", len(hits), kind)
	single := len(hits) == 1
	if !single {
		p.pushPicker = true
	}
	hit := hits[0]
	p.mu.Unlock()

	if single {
		if err := p.load(hit.Path, hit.Line); err != nil {
			p.mu.Lock()
			p.status = err.Error()
			p.mu.Unlock()
		}
	}
	p.wake()
}

// position snapshots the cursor as a language-server request. line is 1-based
// and col counts UTF-16 code units, the JavaScript convention servers expect.
func (p *Pane) position() (abs, rel string, line, col int, why string) {
	if p.mgr == nil {
		return "", "", 0, 0, "no language server support"
	}
	if p.abs == "" || len(p.lines) == 0 {
		return "", "", 0, 0, "no file open"
	}
	return p.abs, p.rel, p.line + 1, utf16Col(p.lineText(), p.col), ""
}

func (p *Pane) wake() {
	if p.onWake != nil {
		p.onWake()
	}
}

func (p *Pane) notify(msg string) {
	if msg != "" && p.onToast != nil {
		p.onToast(msg)
	}
}

// ------------------------------------------------------------ file loading

// load reads path and moves the cursor to line (1-based, 0 = top). It may run
// on a query goroutine, so it takes the lock only to swap state in.
func (p *Pane) load(path string, line int) error {
	abs := p.resolve(path)
	lines, err := readFileLines(abs)
	if err != nil {
		return err
	}

	p.mu.Lock()
	previous := p.abs
	p.mu.Unlock()

	hl := p.highlightLines(abs, lines)

	p.mu.Lock()
	defer p.mu.Unlock()
	p.abs = abs
	p.rel = relPath(p.cwd, abs)
	p.lines = lines
	p.hl = hl
	p.loadErr = ""
	p.status = ""
	p.selecting = false
	if previous != "" && previous != abs {
		// The old document would otherwise stay open in the server for the
		// rest of the session. Off the UI goroutine: it is a pipe write.
		mgr := p.mgr
		if mgr != nil {
			go mgr.CloseDoc(previous)
		}
		p.matches = nil // search hits belong to the file that was open
	}
	if !p.active {
		return nil
	}
	p.line = clampLine(line-1, len(lines))
	// Land the caret on the code, not in the indentation: a definition question
	// asked from column 0 finds nothing on an indented line.
	p.col = firstNonBlank(p.lineText())
	p.scroll = 0
	p.xScroll = 0
	p.clamp()
	return nil
}

// highlightLines tokenises with the current theme, retrying if the theme moved
// while chroma was working.
func (p *Pane) highlightLines(abs string, lines []string) map[int][]components.Span {
	for {
		p.mu.Lock()
		th, ver := p.theme, p.themeVer
		p.mu.Unlock()

		hl := codeview.Highlight(abs, lines, th)

		p.mu.Lock()
		same := p.themeVer == ver
		p.mu.Unlock()
		if same {
			return hl
		}
	}
}

// reload re-reads the open file. It runs with the mutex dropped because load
// takes it, so it reads the target first and then loads outside the lock.
func (p *Pane) reload() string {
	p.mu.Lock()
	abs, line := p.abs, p.line+1
	p.mu.Unlock()

	if abs == "" {
		return "no file open"
	}
	if err := p.load(abs, line); err != nil {
		return err.Error()
	}
	return "reloaded"
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
// copied locations are shown and pasted on every platform, and "src\\a.go:2"
// is worthless in a terminal or editor on the other OS.
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

// ------------------------------------------------------------ cursor

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

// firstNonBlank is the column a jump lands on: the first character that is not
// indentation, so a definition question starts on the symbol rather than in
// whitespace where no server can answer.
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

// utf16Col converts a byte offset into the UTF-16 code-unit column that LSP
// positions use.
func utf16Col(s string, col int) int {
	if col <= 0 {
		return 0
	}
	if col > len(s) {
		col = len(s)
	}
	return len(utf16.Encode([]rune(s[:clampCol(s, col)])))
}

func gutterWidth(lines int) int {
	return len(strconv.Itoa(max(lines, 1))) + 3
}

// ------------------------------------------------------------ search

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
		p.line = p.matches[0]
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

// ------------------------------------------------------------ selection

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

// addRef stages the selected lines (or the caret line alone) for the chat input
// and reports the outcome as a toast.
func (p *Pane) addRef() string {
	ref, ok := p.selectedRef()
	if !ok {
		return "no file open"
	}
	if p.onRef == nil {
		return "chat input is unavailable"
	}
	p.selecting = false
	p.pendingRef = &ref // delivered by Handle, outside the mutex
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
	return chat.Ref{
		Path:  p.rel,
		Start: lo + 1,
		End:   hi + 1,
		Lang:  fenceLang(p.abs),
		Text:  strings.Join(p.lines[lo:hi+1], "\n"),
	}, true
}

// fenceLang is the markdown fence hint for a path: "main.go" -> "go".
func fenceLang(path string) string {
	return strings.TrimPrefix(filepath.Ext(path), ".")
}

// ------------------------------------------------------------ copy / picker

// copyLine and copyLocation stage the text: the callback runs after Handle
// drops the mutex, the same rule the toast path follows.
func (p *Pane) copyLine() string {
	if p.onCopy == nil {
		return ""
	}
	p.pendingCopy = p.lineText()
	return ""
}

func (p *Pane) copyLocation() string {
	if p.onCopy == nil {
		return ""
	}
	p.pendingCopy = fmt.Sprintf("%s:%d", p.rel, p.line+1)
	return ""
}

// copy hands staged text to the clipboard callback and reports the outcome.
func (p *Pane) copy(text string) string {
	if p.onCopy(text) {
		return "copied"
	}
	return "copy failed"
}

func (p *Pane) pickerOpen() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.picker.Open
}

// openPicker shows whatever the last answer produced. UI goroutine only.
func (p *Pane) openPicker() {
	kind := p.navKind
	if p.outline != nil {
		kind = "outline"
	}
	items := make([]listpicker.Item, 0, max(len(p.navHits), len(p.outline)))
	if p.outline != nil {
		for i, s := range p.outline {
			items = append(items, listpicker.Item{
				ID:      strconv.Itoa(i),
				Primary: strings.Repeat(" ", s.Indent) + s.Name,
				Detail:  s.Kind,
			})
		}
	} else {
		for i, h := range p.navHits {
			items = append(items, listpicker.Item{
				ID:      strconv.Itoa(i),
				Primary: fmt.Sprintf("%s:%d", h.Path, h.Line),
				Detail:  strings.TrimSpace(h.Pre + h.Mid + h.Post),
			})
		}
	}
	if len(items) == 0 {
		return
	}
	p.picker.Show(items, listpicker.ShowConfig{
		Title:      kind,
		FilterHint: "filter",
		Empty:      "no results",
	})
}

// acceptPicker jumps to a picked answer. It is invoked by the picker on the UI
// goroutine, so it takes the mutex only to read the target and then loads the
// file outside it.
func (p *Pane) acceptPicker(item listpicker.Item) {
	i, err := strconv.Atoi(item.ID)
	if err != nil {
		return
	}

	p.mu.Lock()
	if p.outline != nil {
		if i >= 0 && i < len(p.outline) {
			sym := p.outline[i]
			p.line = clampLine(sym.Line-1, len(p.lines))
			p.col = firstNonBlank(p.lineText())
			p.scroll = 0
			p.reveal()
		}
		p.mu.Unlock()
		return
	}
	if i < 0 || i >= len(p.navHits) {
		p.mu.Unlock()
		return
	}
	hit := p.navHits[i]
	p.mu.Unlock()

	if err := p.load(hit.Path, hit.Line); err != nil {
		p.mu.Lock()
		p.status = err.Error()
		p.mu.Unlock()
	}
}

// ------------------------------------------------------------ draw

// Draw paints the overlay.
func (p *Pane) Draw(ctx components.DrawContext) components.Surface {
	if p == nil {
		return components.NewSurface(ctx.Max.Width, ctx.Max.Height, nil)
	}
	p.mu.Lock()
	p.method = ctx.Method
	p.viewW = ctx.Max.Width
	p.viewH = max(ctx.Max.Height-2, 1)
	if p.pushPicker {
		p.pushPicker = false
		p.openPicker()
	}
	p.reveal()
	snap := p.snapshot(ctx)
	showPicker := p.picker.Open
	card := p.card
	th := p.theme
	p.mu.Unlock()

	surf := codeview.Paint(ctx, snap)
	if card != nil {
		if sub, ok := paintCard(ctx, th, card); ok {
			surf.Children = append(surf.Children, sub)
		}
	}
	if showPicker {
		surf.Children = append(surf.Children, components.SubSurface{
			Origin:  components.Point{X: 0, Y: 0},
			Z:       cardZ + 10,
			Surface: p.picker.Draw(ctx),
		})
	}
	return surf
}

func (p *Pane) snapshot(ctx components.DrawContext) codeview.Model {
	status := p.status
	switch {
	case p.loadErr != "":
		status = p.loadErr
	case p.searchMode || p.searchQuery != "":
		// The query has to be visible somewhere while it is being typed.
		status = p.searchStatus()
	case p.selecting:
		lo, hi := p.selectionLines()
		status = fmt.Sprintf("%d lines selected%s a to add", hi-lo+1, chrome.Sep)
	case status == "":
		status = p.location()
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
		Help:       p.help,
		Empty:      p.emptyText(),
	}
	if p.selecting {
		// Line-wise: the moving end is pinned past any real line so the
		// tint is clipped at the frame edge rather than stopping mid-line.
		lo, hi := p.selectionLines()
		m.Selecting = true
		m.SelStart = components.Point{X: 0, Y: lo}
		m.SelEnd = components.Point{X: selLineWide, Y: hi}
	}
	return m
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

func (p *Pane) title() string {
	if p.rel == "" {
		return "no file"
	}
	if p.busy {
		return p.rel + " …"
	}
	return p.rel
}

// location is the resting status text: path, caret, language-server state.
func (p *Pane) location() string {
	if p.abs == "" {
		return ""
	}
	parts := []string{fmt.Sprintf("%s:%d:%d", p.rel, p.line+1, p.col+1)}
	if p.mgr != nil {
		state, msg := p.mgr.State(p.rel)
		switch state {
		case lsp.StateReady:
			parts = append(parts, "lsp "+msg)
		case lsp.StateStarting:
			parts = append(parts, "lsp starting")
		case lsp.StateFailed:
			parts = append(parts, "lsp failed: "+msg)
		}
	}
	parts = append(parts, fmt.Sprintf("%d lines", len(p.lines)))
	return strings.Join(parts, chrome.Sep)
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

// paintCard renders the hover box: signature, then the server's markdown doc.
func paintCard(ctx components.DrawContext, th components.Theme, c *hoverCard) (components.SubSurface, bool) {
	maxW, maxH := ctx.Max.Width, ctx.Max.Height
	innerW := min(maxW-6, 92)
	if innerW < cardMinW || maxH < 8 {
		return components.SubSurface{}, false
	}
	var spans []components.Span
	if c.signature != "" {
		spans = append(
			spans,
			components.Span{Text: c.signature, Style: th.TitleOrForeground()},
			components.Span{Text: "\n"},
		)
	}
	if c.doc != "" {
		spans = append(spans, text.RenderMarkdown(c.doc, th)...)
	}
	rows := components.WrapSpans(spans, innerW, ctx.Method)
	maxRows := min(len(rows), min(maxH-6, cardMaxRow))
	if maxRows < 1 {
		return components.SubSurface{}, false
	}
	truncated := len(rows) > maxRows

	boxW := innerW + 4
	boxH := maxRows + 2
	panel := components.NewSurface(boxW, boxH, nil)
	fill := xui.Style{Fg: th.Foreground.Fg}
	for y := range boxH {
		for x := range boxW {
			panel.SetCell(x, y, xui.Cell{Char: " ", Width: 1, Style: fill})
		}
	}
	layout.DrawRoundedBorder(
		&panel,
		layout.BorderRounded,
		chrome.ModalBorder(th),
		&layout.BorderLabel{Text: " " + c.title + " ", Style: chrome.PanelTitle(th)},
		nil,
		nil,
		nil,
		ctx.Method,
	)
	for i := range maxRows {
		components.PaintSpans(&panel, 2, i+1, codeview.ClipSpans(rows[i], innerW, ctx.Method), ctx.Method)
	}
	if truncated {
		panel.Print(2, boxH-2, "(truncated)", th.Muted, ctx.Method)
	}
	ox := max((maxW-boxW)/2, 0)
	oy := max((maxH-boxH)/3, 1)
	return components.SubSurface{
		Origin:  components.Point{X: ox, Y: oy},
		Z:       cardZ,
		Surface: panel,
	}, true
}

// hintLine is advertised at the bottom and expanded by the help modal, so the
// two cannot drift.
var hintLine = strings.Join([]string{
	"esc close",
	"j/k move",
	"h/l scroll",
	"/ find",
	"n/N match",
	"enter def",
	"gr refs",
	"K hover",
	"o outline",
	"v select",
	"a add",
	"y copy",
	"r reload",
	"? help",
}, chrome.Sep)
