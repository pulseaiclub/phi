package sloppy

import (
	"errors"
	"slices"
	"strings"
)

// Parse compiles a sloppy payload into ordered file sections. Sections keep
// first-seen path order; later headers for the same path extend that section.
// A target with no operations is dropped, and a payload of nothing but targets
// is a syntax error.
func Parse(input string) ([]Section, error) {
	src := trimOuterFence(input)
	p := &parser{lx: NewLexer(src), src: src, byPath: make(map[string]*Section)}
	return p.run()
}

type bodyState uint8

const (
	stateIdle bodyState = iota
	stateFind
	statePut
)

// body is the FIND body under construction: the lines as authored plus the
// payload line each came from.
type body struct {
	text []string
	line []int
}

// add records one authored FIND line.
func (b *body) add(text string, line int) {
	b.text = append(b.text, text)
	b.line = append(b.line, line)
}

// reset drops the accumulated lines, keeping the backing arrays.
func (b *body) reset() {
	b.text, b.line = b.text[:0], b.line[:0]
}

// origin maps one joined FIND line back to the payload line it came from.
type origin struct {
	start  int // byte offset of the line within the joined body
	length int // byte length of the joined line, without its newline
	line   int // 1-based payload line
	prefix int // read-output numbering bytes stripped from the payload line
}

// join trims blank edges — a pattern quotes current text, so blank lines
// pasted around it are noise — strips uniform read-output line numbering, and
// joins the lines. It also reports where each joined line came from, so a
// pattern error can name the payload line and column the model wrote.
func (b *body) join() (string, []origin) {
	start, end := 0, len(b.text)
	for start < end && strings.TrimSpace(b.text[start]) == "" {
		start++
	}
	for end > start && strings.TrimSpace(b.text[end-1]) == "" {
		end--
	}
	lines, lns := b.text[start:end], b.line[start:end]
	strip := allNumbered(lines)
	var (
		joined strings.Builder
		orig   = make([]origin, 0, len(lines))
	)
	for i, line := range lines {
		if i > 0 {
			joined.WriteByte('\n')
		}
		text := line
		if strip {
			text = stripNumbered(line)
		}
		orig = append(orig, origin{
			start: joined.Len(), length: len(text), line: lns[i], prefix: len(line) - len(text),
		})
		joined.WriteString(text)
	}
	return joined.String(), orig
}

type parser struct {
	lx        *Lexer
	src       string
	order     []*Section
	byPath    map[string]*Section
	target    *Section
	all       bool
	state     bodyState
	find      body
	put       []string
	putKind   RewriteKind
	havePut   bool
	anchorLn  int // payload line of the open FIND anchor
	putLn     int // payload line of the open action header
	firstPath int // payload line of the first *** SM:EDIT carrying a path
	ops       int
	started   bool
}

func (p *parser) run() ([]Section, error) {
	for p.lx.Next() {
		tok := p.lx.Token()
		// An *** SM:AFTER body is content: its lines pass through verbatim,
		// header-shaped or not. A near-miss header names no operation: it
		// would have opened one.
		if tok.Kind == TokenText && !p.raw() {
			if diag, ok := badHeader(tok.Text); ok {
				return nil, p.diag(0, tok.Line, 0, 0, diag.code, diag.msg, diag.label, diag.help)
			}
		}
		if !p.started {
			if tok.Kind == TokenText && strings.TrimSpace(tok.Text) == "" {
				continue // leading blank lines
			}
			if tok.Kind != TokenEdit || tok.Path == "" {
				return nil, p.startDiag(tok)
			}
		}
		var err error
		switch tok.Kind {
		case TokenEdit:
			if err = p.flush(); err == nil {
				p.started = true
				p.all = tok.All
				if tok.Path != "" {
					if p.firstPath == 0 {
						p.firstPath = tok.Line
					}
					p.target = p.section(tok.Path)
				}
			}
		case TokenFind:
			if err = p.flush(); err == nil {
				p.state = stateFind
				p.anchorLn = tok.Line
			}
		case TokenPut, TokenAfter:
			if p.havePut {
				err = p.duplicateDiag(tok)
			} else {
				p.havePut = true
				p.putKind = RewriteReplace
				if tok.Kind == TokenAfter {
					p.putKind = RewriteInsert
				}
				p.putLn = tok.Line
				p.state = statePut
			}
		case TokenText:
			p.append(tok)
		}
		if err != nil {
			return nil, err
		}
	}
	if err := p.flush(); err != nil {
		return nil, err
	}
	sections := make([]Section, 0, len(p.order))
	for _, s := range p.order {
		if len(s.Ops) > 0 {
			sections = append(sections, *s)
		}
	}
	if len(sections) == 0 && p.started {
		return nil, p.diag(0, p.firstPath, 0, 0, codeNoOps,
			"file section has no operations",
			"this *** SM:EDIT opens a section that nothing edits",
			"follow it with *** SM:FIND <current text> and *** SM:PUT <final text> (or *** SM:AFTER <lines>); "+
				"use the write tool to state a whole file.")
	}
	return sections, nil
}

// raw reports whether the parser is inside an *** SM:AFTER body, whose lines
// are content rather than payload syntax.
func (p *parser) raw() bool { return p.state == statePut && p.putKind == RewriteInsert }

// pendingOp is the operation a diagnostic inside the open body belongs to, or
// 0 while the payload still has no file target.
func (p *parser) pendingOp() int {
	if !p.started {
		return 0
	}
	return p.ops + 1
}

// append routes a body line to the open FIND or action buffer, dropping
// read-output chrome such as "[Showing lines 1-20 of 80]".
func (p *parser) append(tok Token) {
	line := tok.Text
	if isBodyNoise(strings.TrimSpace(line)) {
		return
	}
	switch p.state {
	case statePut:
		p.put = append(p.put, line)
	case stateFind:
		p.find.add(line, tok.Line)
	default:
		p.state = stateFind
		p.anchorLn = tok.Line
		p.find.add(line, tok.Line)
	}
}

// flush closes the open operation, if any, at the next header or end of input.
func (p *parser) flush() error {
	find, orig := p.find.join()
	put := joinAction(p.put)
	anchor, putLn := p.anchorLn, p.putLn
	kind, havePut := p.putKind, p.havePut
	p.find.reset()
	p.put = p.put[:0]
	p.havePut = false
	p.state = stateIdle
	p.anchorLn, p.putLn = 0, 0

	op := Operation{Number: p.ops + 1, All: p.all}
	switch {
	case find == "" && !havePut:
		return nil
	case find == "":
		if kind == RewriteInsert {
			return p.diag(op.Number, putLn, 0, 0, codeNoAnchor,
				"*** SM:AFTER has no *** SM:FIND anchor",
				"this header needs a *** SM:FIND above it",
				"*** SM:AFTER inserts below a match: write *** SM:FIND <current text>, then *** SM:AFTER <lines>. "+
					"To state a whole file, use *** SM:PUT directly under *** SM:EDIT.")
		}
		if put == "" {
			return nil
		}
		// An anchor-less PUT asserts desired file content.
		op.Desired = true
		op.Line = putLn
		op.Rewrite = Rewrite{Kind: RewriteReplace, Text: put}
	default:
		pattern, err := scanPattern(find)
		if err != nil {
			return p.bodyDiag(op.Number, err, orig, anchor)
		}
		op.Pattern = pattern
		op.Line = anchor
		switch {
		case havePut:
			op.Rewrite = Rewrite{Kind: kind, Text: put}
		case len(pattern.Selections) > 0:
			op.Rewrite = Rewrite{Kind: RewriteInline}
		default:
			return p.diag(op.Number, anchor, 0, 0, codeNoAction,
				"*** SM:FIND body is never rewritten",
				"this anchor has no *** SM:PUT, no *** SM:AFTER and no ⟪old│new⟫ selection",
				"add *** SM:PUT <final text> (an empty body deletes the match) or *** SM:AFTER <lines to insert>; "+
					"a body line that spells a recognized header is one, not content; "+
					"use write for files that embed this syntax; "+
					"an inline ⟪old│new⟫ selection needs no action header.")
		}
	}
	p.ops++
	p.target.Ops = append(p.target.Ops, op)
	return nil
}

// section returns the section for path, creating it on first use.
func (p *parser) section(path string) *Section {
	if s, ok := p.byPath[path]; ok {
		return s
	}
	s := &Section{Path: path}
	p.byPath[path] = s
	p.order = append(p.order, s)
	return s
}

// diag builds a diagnostic anchored at a payload span. A width of 0 underlines
// the rest of the line; a line of 0 renders no snippet.
func (p *parser) diag(op, line, col, width int, code, msg, label, help string) error {
	return &ParseError{
		Code: code, Op: op, Line: line, Col: col, Width: width,
		Src: p.src, Msg: msg, Label: label, Help: help,
	}
}

// startDiag explains a payload that does not open with a file target.
func (p *parser) startDiag(tok Token) error {
	if tok.Kind == TokenEdit {
		return p.diag(0, tok.Line, 0, 0, codeNoPath,
			"*** SM:EDIT has no file path",
			"expected a path after the header",
			`write *** SM:EDIT relative/path.ts — e.g. *** SM:EDIT src/app.ts. `+
				`JSON-quote paths with spaces: *** SM:EDIT "src/my file.ts".`)
	}
	label := "expected *** SM:EDIT <path> here, not file content"
	switch tok.Kind {
	case TokenFind:
		label = "*** SM:FIND needs a *** SM:EDIT <path> above it"
	case TokenPut, TokenAfter:
		label = "this action needs a *** SM:EDIT <path> above it"
	}
	return p.diag(0, tok.Line, 0, 0, codeNoTarget,
		"payload does not open with a file target",
		label,
		"open the payload with *** SM:EDIT relative/path.ts, then *** SM:FIND <current text> and "+
			"*** SM:PUT <final text>; repeat the *** SM:EDIT header for each file.")
}

// duplicateDiag explains a second action header with no anchor between them.
func (p *parser) duplicateDiag(tok Token) error {
	word := tok.Kind.String()
	return p.diag(p.pendingOp(), tok.Line, 0, 0, codeDupAction,
		"duplicate *** "+word+" header",
		"the previous action is still open: no *** SM:FIND between the two",
		"give the second action its own *** SM:FIND, or fold its lines into the *** SM:PUT body: the body is the "+
			"final text.")
}

// bodyDiag anchors a malformed FIND body at the payload span that broke it.
func (p *parser) bodyDiag(op int, err error, orig []origin, anchor int) error {
	var pattern *patternError
	if !errors.As(err, &pattern) {
		pattern = &patternError{Code: codePatternScan, Msg: err.Error()}
	}
	line, col, width := anchor, 0, 0
	if l, c, w, found := locate(orig, pattern.Offset, pattern.Width); found {
		line, col, width = l, c, w
	}
	return p.diag(op, line, col, width, pattern.Code, pattern.Msg, pattern.Label, pattern.Help)
}

// locate maps a byte offset inside the joined FIND body back to the payload
// line the model wrote, the byte offset within it, and the span width. It
// reports false when the offset lands outside the authored lines.
func locate(orig []origin, off, width int) (line, col, span int, found bool) {
	for _, o := range slices.Backward(orig) {
		if off < o.start {
			continue
		}
		at := off - o.start
		if at > o.length {
			return 0, 0, 0, false // the offset is on the newline joining two lines
		}
		return o.line, o.prefix + at, min(width, o.length-at), true
	}
	return 0, 0, 0, false
}

// joinAction joins a rewrite body, keeping blank edges: in *** SM:PUT and
// *** SM:AFTER an authored blank line is content.
func joinAction(lines []string) string {
	return joinLines(lines)
}

// joinLines strips uniform read-output line numbering and joins body lines.
func joinLines(body []string) string {
	if len(body) == 0 {
		return ""
	}
	if allNumbered(body) {
		var b strings.Builder
		for i, line := range body {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(stripNumbered(line))
		}
		return b.String()
	}
	return strings.Join(body, "\n")
}

// allNumbered reports whether every line carries a read-output line number.
func allNumbered(lines []string) bool {
	for _, line := range lines {
		if _, ok := splitNumbered(line); !ok {
			return false
		}
	}
	return len(lines) > 0
}

// stripNumbered removes the read-output line-number prefix from line.
func stripNumbered(line string) string {
	rest, ok := splitNumbered(line)
	if !ok {
		return line
	}
	return rest
}

// splitNumbered splits "12| code" or "12: code" into its content, reporting
// whether the line carried a numeric prefix.
func splitNumbered(line string) (string, bool) {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	digits := i
	for i < len(line) && '0' <= line[i] && line[i] <= '9' {
		i++
	}
	if i == digits || i == len(line) {
		return "", false
	}
	if line[i] != '|' && line[i] != ':' {
		return "", false
	}
	i++
	if i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return line[i:], true
}

// isBodyNoise reports whether a body line is read-output chrome rather than
// content: "[Showing lines 1-20 of 80]", "[3 more lines in a.ts. use read to
// continue]", or an elided-line marker such as "12-14: …".
func isBodyNoise(line string) bool {
	if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
		inner := line[1 : len(line)-1]
		switch {
		case hasPrefixFold(inner, "showing lines"):
			return true
		case strings.Contains(inner, " more line") &&
			strings.Contains(inner, " in ") &&
			strings.Contains(inner, ". use ") &&
			hasSuffixFold(inner, "to continue"):
			return true
		}
	}
	return isElidedMarker(line)
}

// isElidedMarker reports whether line is a read-output elision marker such as
// "12: …" or "12-14: ...".
func isElidedMarker(line string) bool {
	i := 0
	digits := func() bool {
		start := i
		for i < len(line) && '0' <= line[i] && line[i] <= '9' {
			i++
		}
		return i > start
	}
	if !digits() {
		return false
	}
	if i < len(line) && line[i] == '-' {
		i++
		if !digits() {
			return false
		}
	}
	if i == len(line) || line[i] != ':' {
		return false
	}
	rest := strings.TrimSpace(line[i+1:])
	return rest == gapMarker || rest == "..."
}

// hasSuffixFold reports whether s ends with suffix under ASCII
// case-insensitive matching without allocating.
func hasSuffixFold(s, suffix string) bool {
	return len(s) >= len(suffix) && strings.EqualFold(s[len(s)-len(suffix):], suffix)
}

// trimOuterFence removes a markdown fence wrapping the whole payload, keeping
// the fenced line's slot as a blank line so reported payload line numbers stay
// the ones the model wrote.
func trimOuterFence(input string) string {
	first := input
	rest := ""
	broken := false
	if before, after, ok := strings.Cut(input, "\n"); ok {
		first, rest, broken = before, after, true
	}
	if !isOuterFence(strings.TrimSpace(first)) {
		return input
	}
	inner := strings.TrimRight(rest, " \t\r\n")
	inner = strings.TrimRight(strings.TrimSuffix(inner, "```"), " \t\r\n")
	if !broken {
		return inner
	}
	return "\n" + inner
}

// isOuterFence reports whether line opens a bare markdown fence, with or
// without one of the language tags models commonly use for payloads.
func isOuterFence(line string) bool {
	if !strings.HasPrefix(line, "```") {
		return false
	}
	tag := strings.TrimSpace(line[3:])
	switch strings.ToLower(tag) {
	case "", "text", "xml", "html", "typescript", "ts", "tsx", "javascript", "js":
		return true
	}
	return false
}
