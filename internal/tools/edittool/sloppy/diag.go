package sloppy

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Diagnostic codes give every payload syntax failure a stable name, so a
// report can be recognized without re-reading its prose.
const (
	codeNoTarget     = "SM101" // payload does not open with *** SM:EDIT <path>
	codeNoPath       = "SM102" // *** SM:EDIT carries no file path
	codeNoAction     = "SM103" // *** SM:FIND body is never rewritten
	codeNoAnchor     = "SM104" // action header before any *** SM:FIND
	codeDupAction    = "SM105" // second action header with no *** SM:FIND between
	codeHeaderWord   = "SM106" // unrecognized header keyword
	codeHeaderArg    = "SM107" // argument on a header that takes none
	codeEditPath     = "SM108" // unreadable *** SM:EDIT path
	codeHeaderShape  = "SM109" // malformed header marker
	codeNoOps        = "SM110" // file section without operations
	codePatternScan  = "SM200" // malformed FIND body
	codePatternOpen  = "SM201" // ⟪ is never closed
	codePatternStray = "SM202" // ⟫ outside a selection
	codePatternDiv   = "SM203" // selection without the │ divider
	codePatternMulti = "SM204" // selection with more than one │ divider
	codePatternNest  = "SM205" // ⟪ nested inside a selection
)

// headerWords are the recognized header keywords, in payload order.
var headerWords = []string{"EDIT", "FIND", "PUT", "AFTER"}

// ParseError is one payload syntax failure, rendered as a compiler-style
// report: the offending payload line, a caret under the offending span, and
// the fix. Reported lines count the payload the model wrote, never file lines.
type ParseError struct {
	Code  string // diagnostic code, e.g. "SM103"
	Op    int    // 1-based operation number; 0 when the failure is payload-wide
	Line  int    // 1-based payload line; 0 renders no snippet
	Col   int    // 0-based byte offset of the span within Line
	Width int    // span width in bytes; 0 underlines the rest of Line
	Src   string // payload Line counts into
	Msg   string // headline: what is broken
	Label string // caret label: what the marked span is
	Help  string // one actionable fix
}

// Error implements the error interface.
func (e *ParseError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "error[%s]: %s", e.Code, e.Msg)
	if e.Op > 0 {
		fmt.Fprintf(&b, " (operation %d)", e.Op)
	}
	if line, ok := e.line(); ok {
		gutter := len(strconv.Itoa(e.Line))
		pad := strings.Repeat(" ", gutter)
		prefix := e.prefix(line)
		fmt.Fprintf(&b, "\n%s--> payload line %d:%d", pad, e.Line, utf8.RuneCountInString(prefix)+1)
		fmt.Fprintf(&b, "\n%s |", pad)
		fmt.Fprintf(&b, "\n%*d | %s", gutter, e.Line, line)
		fmt.Fprintf(&b, "\n%s | %s%s", pad, caretIndent(prefix), carets(line[len(prefix):], e.Width))
		if e.Label != "" {
			b.WriteString(" " + e.Label)
		}
	}
	if e.Help != "" {
		b.WriteString("\nhelp: " + e.Help)
	}
	return b.String()
}

// line returns the payload line the report points at.
func (e *ParseError) line() (string, bool) {
	if e.Line < 1 || e.Src == "" {
		return "", false
	}
	rest := e.Src
	for n := 1; n < e.Line; n++ {
		i := strings.IndexByte(rest, '\n')
		if i < 0 {
			return "", false
		}
		rest = rest[i+1:]
	}
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimSuffix(rest, "\r"), true
}

// prefix returns the part of line the span starts after.
func (e *ParseError) prefix(line string) string {
	col := min(max(e.Col, 0), len(line))
	for col > 0 && col < len(line) && !utf8.RuneStart(line[col]) {
		col--
	}
	return line[:col]
}

// caretIndent blanks out the text before the span. Tabs stay tabs so the caret
// keeps its column under tab-indented payload lines.
func caretIndent(prefix string) string {
	var b strings.Builder
	b.Grow(len(prefix))
	for _, r := range prefix {
		if r == '\t' {
			b.WriteByte('\t')
			continue
		}
		b.WriteByte(' ')
	}
	return b.String()
}

// carets underlines width bytes of rest, or all of it when width is 0.
func carets(rest string, width int) string {
	if width > 0 {
		rest = rest[:min(width, len(rest))]
	}
	return strings.Repeat("^", max(1, utf8.RuneCountInString(strings.ToValidUTF8(rest, ""))))
}

// headerDiag is a line that opens like a header but is not one.
type headerDiag struct {
	code, msg, label, help string
}

// badHeader classifies line as a near-miss header, reporting false when the
// line is ordinary body content. Catching these beats letting them fall
// through as file text, where a one-character typo surfaces as a confusing
// match failure far from its cause.
func badHeader(line string) (headerDiag, bool) {
	const sm = "SM:"
	trimmed := strings.TrimSpace(line)
	stars := 0
	for stars < len(trimmed) && trimmed[stars] == '*' {
		stars++
	}
	rest := strings.TrimLeft(trimmed[min(stars, len(trimmed)):], " \t")
	if stars < 2 || !hasPrefixFold(rest, sm) {
		return headerDiag{}, false
	}
	if stars != 3 || (stars < len(trimmed) && trimmed[stars] != ' ' && trimmed[stars] != '\t') {
		return headerDiag{
			code:  codeHeaderShape,
			msg:   "malformed header marker",
			label: "a header is *** plus one space",
			help:  "write headers as *** SM:FIND: three asterisks, one space, then the keyword.",
		}, true
	}
	after := rest[len(sm):]
	name, arg := after, ""
	if i := strings.IndexAny(after, " \t"); i >= 0 {
		name, arg = after[:i], strings.TrimSpace(after[i:])
	}
	if name == "" {
		return headerDiag{
			code:  codeHeaderWord,
			msg:   "header has no keyword",
			label: "expected EDIT, FIND, PUT or AFTER after SM:",
			help:  "write *** SM:EDIT, *** SM:FIND, *** SM:PUT or *** SM:AFTER.",
		}, true
	}
	for _, word := range headerWords {
		if !strings.EqualFold(name, word) {
			continue
		}
		if word == "EDIT" {
			if _, _, ok := parseEditArg(arg); ok {
				return headerDiag{}, false
			}
			return headerDiag{
				code:  codeEditPath,
				msg:   "*** SM:EDIT path is unreadable",
				label: "not a valid file path",
				help: `quote paths with spaces as *** SM:EDIT "src/my file.ts" and keep the quoting balanced, ` +
					`or drop the quotes.`,
			}, true
		}
		if arg != "" {
			return headerDiag{
				code:  codeHeaderArg,
				msg:   "*** SM:" + word + " takes no argument",
				label: strconv.Quote(arg) + " has no meaning here",
				help:  "\"all\" belongs on the file header: *** SM:EDIT src/app.ts all.",
			}, true
		}
		return headerDiag{}, false // the lexer already recognized it
	}
	help := "headers are *** SM:EDIT, *** SM:FIND, *** SM:PUT and *** SM:AFTER."
	if near := closestHeader(name); near != "" {
		help = "Did you mean *** SM:" + near + "? " + help
	}
	return headerDiag{
		code:  codeHeaderWord,
		msg:   "unknown header keyword " + strconv.Quote(name),
		label: "not a header keyword",
		help:  help,
	}, true
}

// closestHeader returns the header keyword nearest to name, within two edits,
// for a "did you mean" hint. It reports "" when nothing is close enough.
func closestHeader(name string) string {
	best, bestDistance := "", 3
	for _, word := range headerWords {
		if d := editDistance(strings.ToUpper(name), word); d < bestDistance {
			best, bestDistance = word, d
		}
	}
	return best
}

// editDistance is the Levenshtein distance between two ASCII words.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(min(curr[j-1]+1, prev[j]+1), prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}
