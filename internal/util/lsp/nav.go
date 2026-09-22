package lsp

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Hit is one navigation result, shaped like a search match so a caller can
// render language-server answers and its own hits with the same code.
type Hit struct {
	Path string // relative to the root, or absolute when outside it
	Line int    // 1-based
	Pre  string
	Mid  string
	Post string
	Def  bool
	Ext  bool // outside the workspace root, e.g. the standard library
}

// Symbol is one entry of a document outline.
type Symbol struct {
	Name   string
	Kind   string
	Line   int // 1-based
	Indent int
}

// Hover is what a server says about one position: a signature to show and the
// documentation that came with it.
type Hover struct {
	Signature string // plain text: HTML tags stripped, no highlighting
	Doc       string // raw markdown, as the server sent it
	Empty     bool
}

// utf16ToByte converts a column measured in UTF-16 code units (what JavaScript
// string indexes count) into a byte offset into the same line.
func utf16ToByte(line string, u16 int) int {
	if u16 <= 0 {
		return 0
	}
	units, bytes := 0, 0
	for _, r := range line {
		if units >= u16 {
			break
		}
		units += len(utf16.Encode([]rune{r}))
		bytes += utf8.RuneLen(r)
	}
	if bytes > len(line) {
		return len(line)
	}
	return bytes
}

// relPath maps an absolute path to what a Hit reports and a caller opens:
// relative to the workspace root when it is inside, absolute when it is not
// (the standard library, the module cache).
func (m *Manager) relPath(abs string) (rel string, ext bool) {
	rel, err := filepath.Rel(m.root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(abs), true
	}
	return filepath.ToSlash(rel), false
}

// readLines reads a file into lines for snippet rendering. \r\n is normalised
// so positions and column maths agree with what editors show. An unreadable
// file is not an error: the hit still names the path, it just has no text.
func readLines(abs string) []string {
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil
	}
	return readLinesFrom(data)
}

func readLinesFrom(data []byte) []string {
	return strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
}

// lineAt returns the text of a 1-based line, or "" when the file is shorter
// than that or was never read.
func lineAt(lines []string, line int) string {
	if line-1 < 0 || line-1 >= len(lines) {
		return ""
	}
	return lines[line-1]
}

// positionText returns the text of the line a request is asking about. Unlike
// snippet rendering, an unreadable file is an error here: answering with
// character 0 would send the server a different question than the one asked.
func positionText(abs string, line int) (string, error) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return lineAt(readLinesFrom(data), line), nil
}

// resolve turns a set of LSP ranges into display-ready hits, reading every
// target file once.
func (m *Manager) resolve(c *lspClient, locs []lspLocation, markDef bool) []Hit {
	type fileKey struct {
		abs, rel string
		ext      bool
	}
	byFile := map[fileKey][]lspRange{}
	var order []fileKey

	for _, l := range locs {
		abs, err := uriToPath(l.URI)
		if err != nil {
			continue
		}
		rel, ext := m.relPath(abs)
		k := fileKey{abs, rel, ext}
		if _, seen := byFile[k]; !seen {
			order = append(order, k)
		}
		byFile[k] = append(byFile[k], l.Range)
	}

	var out []Hit
	for _, k := range order {
		lines := readLines(k.abs)
		ranges := byFile[k]
		sort.Slice(ranges, func(i, j int) bool {
			if ranges[i].Start.Line != ranges[j].Start.Line {
				return ranges[i].Start.Line < ranges[j].Start.Line
			}
			return ranges[i].Start.Character < ranges[j].Start.Character
		})
		seen := map[int]bool{}
		for _, r := range ranges {
			line, from := c.fromLSP(lines, r.Start)
			if seen[line] {
				continue // one entry per line keeps the list readable
			}
			seen[line] = true

			text := lineAt(lines, line)
			to := from
			if r.End.Line == r.Start.Line {
				_, to = c.fromLSP(lines, r.End)
			}
			if to <= from || to > len(text) {
				to = len(text)
				if from > to {
					from = to
				}
			}
			s := snip([]byte(text), from, to)
			out = append(out, Hit{
				Path: k.rel, Line: line, Pre: s.pre, Mid: s.mid, Post: s.post,
				Def: markDef, Ext: k.ext,
			})
		}
	}
	return out
}

const (
	snipLead = 32  // start eliding once the match sits this far into the line
	snipKeep = 16  // runes of lead-in kept when we do elide
	snipMax  = 240 // cap on the whole snippet
)

// snippet is one raw line split around a match, ready to display.
type snippet struct {
	pre, mid, post string
}

// snip turns one raw line plus a byte range into display-ready pieces, dropping
// indentation and keeping the match itself in view.
func snip(line []byte, from, to int) snippet {
	from = min(max(from, 0), len(line))
	to = min(max(to, from), len(line))
	pre, mid, post := string(line[:from]), string(line[from:to]), string(line[to:])

	if trimmed := strings.TrimLeft(pre, " \t"); trimmed != pre {
		pre = trimmed
	}
	if n := utf8.RuneCountInString(pre); n > snipLead {
		r := []rune(pre)
		pre = "…" + string(r[n-snipKeep:])
	}
	if n := utf8.RuneCountInString(mid); n > snipMax {
		mid = string([]rune(mid)[:snipMax]) + "…"
	}
	if budget := snipMax - utf8.RuneCountInString(pre) - utf8.RuneCountInString(mid); budget > 0 {
		if utf8.RuneCountInString(post) > budget {
			post = string([]rune(post)[:budget]) + "…"
		}
	} else {
		post = ""
	}
	return snippet{pre: pre, mid: mid, post: strings.TrimRight(post, " \t")}
}

// locate runs one position-based request and normalises the two shapes a server
// may answer with (Location or LocationLink).
func (m *Manager) locate(
	ctx context.Context, method, abs, rel string, line, u16col int, extra map[string]any,
) ([]Hit, error) {
	c, err := m.client(ctx, rel)
	if err != nil {
		return nil, err
	}
	if err := c.ensureOpen(abs, rel); err != nil {
		return nil, err
	}

	text, err := positionText(abs, line)
	if err != nil {
		return nil, err
	}
	byteCol := utf16ToByte(text, u16col)

	params := map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(abs)},
		"position":     c.toLSP(text, line, byteCol),
	}
	maps.Copy(params, extra)

	var raw []map[string]any
	if err := c.call(ctx, method, params, &raw); err != nil {
		// A single Location, not an array, is also legal.
		var one lspLocation
		if err2 := c.call(ctx, method, params, &one); err2 != nil || one.URI == "" {
			return nil, err
		}
		return m.resolve(c, []lspLocation{one}, method != "textDocument/references"), nil
	}

	locs := make([]lspLocation, 0, len(raw))
	for _, item := range raw {
		if uri, ok := item["uri"].(string); ok {
			locs = append(locs, lspLocation{URI: uri, Range: decodeRange(item["range"])})
			continue
		}
		if uri, ok := item["targetUri"].(string); ok {
			rng := item["targetSelectionRange"]
			if rng == nil {
				rng = item["targetRange"]
			}
			locs = append(locs, lspLocation{URI: uri, Range: decodeRange(rng)})
		}
	}
	return m.resolve(c, locs, method != "textDocument/references"), nil
}

func decodeRange(v any) lspRange {
	mv, ok := v.(map[string]any)
	if !ok {
		return lspRange{}
	}
	pos := func(key string) lspPosition {
		p, ok := mv[key].(map[string]any)
		if !ok {
			return lspPosition{}
		}
		l, _ := p["line"].(float64)
		c, _ := p["character"].(float64)
		return lspPosition{Line: int(l), Character: int(c)}
	}
	return lspRange{Start: pos("start"), End: pos("end")}
}

// Definition asks the server where the symbol at a position is defined. line is
// 1-based and col counts UTF-16 code units, the JavaScript convention.
func (m *Manager) Definition(ctx context.Context, abs, rel string, line, col int) ([]Hit, error) {
	return m.locate(ctx, "textDocument/definition", abs, rel, line, col, nil)
}

// References asks the server where the symbol at a position is used, including
// its own declaration.
func (m *Manager) References(ctx context.Context, abs, rel string, line, col int) ([]Hit, error) {
	return m.locate(ctx, "textDocument/references", abs, rel, line, col,
		map[string]any{"context": map[string]any{"includeDeclaration": true}})
}

// Symbols returns the document outline, flattened with indentation that mirrors
// the server's nesting.
func (m *Manager) Symbols(ctx context.Context, abs, rel string) ([]Symbol, error) {
	c, err := m.client(ctx, rel)
	if err != nil {
		return nil, err
	}
	if err := c.ensureOpen(abs, rel); err != nil {
		return nil, err
	}

	var raw []lspDocumentSymbol
	if err := c.call(ctx, "textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(abs)},
	}, &raw); err != nil {
		return nil, err
	}

	return flattenSymbols(raw), nil
}

// flattenSymbols turns the two shapes servers answer with (hierarchical
// documentSymbol and flat symbolInformation) into one indented list.
func flattenSymbols(raw []lspDocumentSymbol) []Symbol {
	var out []Symbol
	var walk func(syms []lspDocumentSymbol, depth int)
	walk = func(syms []lspDocumentSymbol, depth int) {
		for _, s := range syms {
			rng := s.SelectionRange
			if s.Location != nil {
				rng = s.Location.Range // symbolInformation form
			}
			out = append(out, Symbol{
				Name: s.Name, Kind: symbolKindName(s.Kind), Line: rng.Start.Line + 1, Indent: depth * 2,
			})
			if len(s.Children) > 0 {
				walk(s.Children, depth+1)
			}
		}
	}
	walk(raw, 0)

	// symbolInformation comes back unordered often enough to be worth fixing.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}

// ---------------------------------------------------------------- hover

// Hover asks the server what a position is, and renders the answer.
func (m *Manager) Hover(ctx context.Context, abs, rel string, line, col int) (*Hover, error) {
	c, err := m.client(ctx, rel)
	if err != nil {
		return nil, err
	}
	if err := c.ensureOpen(abs, rel); err != nil {
		return nil, err
	}
	text, err := positionText(abs, line)
	if err != nil {
		return nil, err
	}
	byteCol := utf16ToByte(text, col)

	var res struct {
		Contents json.RawMessage `json:"contents"`
	}
	err = c.call(ctx, "textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(abs)},
		"position":     c.toLSP(text, line, byteCol),
	}, &res)
	if err != nil {
		return nil, err
	}
	if len(res.Contents) == 0 || string(res.Contents) == "null" {
		return &Hover{Empty: true}, nil
	}

	code, prose := parseHoverContents(res.Contents)
	if code == "" && prose == "" {
		return &Hover{Empty: true}, nil
	}
	return &Hover{Signature: plainSignature(code), Doc: prose}, nil
}

// parseHoverContents flattens the three shapes the spec allows: a MarkupContent
// object, a MarkedString, or an array of MarkedStrings.
func parseHoverContents(raw json.RawMessage) (code, prose string) {
	var markup struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &markup); err == nil && markup.Value != "" {
		return splitMarkdown(markup.Value)
	}

	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return splitMarkdown(one)
	}

	var many []json.RawMessage
	if err := json.Unmarshal(raw, &many); err == nil {
		var codes, proses []string
		for _, item := range many {
			var s string
			if json.Unmarshal(item, &s) == nil {
				c, p := splitMarkdown(s)
				if c != "" {
					codes = append(codes, c)
				}
				if p != "" {
					proses = append(proses, p)
				}
				continue
			}
			var ms struct {
				Language string `json:"language"`
				Value    string `json:"value"`
			}
			if json.Unmarshal(item, &ms) == nil && ms.Value != "" {
				if ms.Language != "" {
					codes = append(codes, ms.Value)
				} else {
					proses = append(proses, ms.Value)
				}
			}
		}
		return strings.Join(codes, "\n"), strings.Join(proses, "\n\n")
	}
	return "", ""
}

// maxProse caps how much documentation a Hover carries.
const maxProse = 900

// splitMarkdown pulls fenced code blocks out of a hover body; what is left is
// documentation prose, which stays markdown because the caller renders it.
func splitMarkdown(s string) (code, prose string) {
	var codes, text []string
	inFence := false
	var fence []string
	for line := range strings.SplitSeq(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inFence {
				codes = append(codes, strings.Join(fence, "\n"))
				fence = nil
			}
			inFence = !inFence
			continue
		}
		if inFence {
			fence = append(fence, line)
		} else {
			text = append(text, line)
		}
	}
	if len(fence) > 0 {
		codes = append(codes, strings.Join(fence, "\n"))
	}
	code = strings.TrimSpace(strings.Join(codes, "\n"))
	prose = strings.TrimSpace(strings.Join(text, "\n"))

	// A server that only speaks plaintext sends no fences at all. Its first
	// line is still the declaration, so promote it rather than showing nothing.
	if code == "" && prose != "" {
		first, rest, _ := strings.Cut(prose, "\n")
		if looksLikeDeclaration(first) {
			code, prose = first, strings.TrimSpace(rest)
		}
	}
	if len(prose) > maxProse {
		if cut := strings.LastIndex(prose[:maxProse], " "); cut > 0 {
			prose = prose[:cut]
		} else {
			prose = prose[:maxProse]
		}
		prose += "…"
	}
	return code, prose
}

// looksLikeDeclaration is a deliberately conservative test: short, no trailing
// sentence punctuation, and carrying syntax a prose line would not.
func looksLikeDeclaration(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 200 || strings.HasSuffix(s, ".") {
		return false
	}
	return strings.ContainsAny(s, "(){}[]<>:=*&") || strings.Contains(s, " ")
}

// maxSignatureLines caps how many lines of a signature a terminal shows.
const maxSignatureLines = 12

// plainSignature renders a signature for a terminal. Some servers mark hover
// types up with HTML spans; a terminal wants the text, not the markup.
func plainSignature(code string) string {
	if code == "" {
		return ""
	}
	code = unescapeHTML(stripTags(code))
	lines := strings.Split(code, "\n")
	if len(lines) > maxSignatureLines {
		lines = append(lines[:maxSignatureLines], "…")
	}
	return strings.Join(lines, "\n")
}

// stripTags drops HTML tags from s. A "<" that cannot start a tag name is kept:
// signatures contain arithmetic, and "< b >" is not a tag.
func stripTags(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '<' {
			b.WriteByte(s[i])
			i++
			continue
		}
		end := strings.IndexByte(s[i:], '>')
		if end < 0 {
			b.WriteString(s[i:])
			break
		}
		if tag := s[i+1 : i+end]; tag == "" || !isTagStart(tag[0]) {
			b.WriteByte('<')
			i++
			continue
		}
		i += end + 1
	}
	return b.String()
}

func isTagStart(c byte) bool {
	return c == '/' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// unescapeHTML turns back the few entities a server puts in a signature.
func unescapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", `"`)
	s = strings.ReplaceAll(s, "&#39;", "'")
	return strings.ReplaceAll(s, "&amp;", "&")
}
