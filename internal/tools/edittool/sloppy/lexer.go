package sloppy

import (
	"encoding/json"
	"strings"
)

// Lexer scans a sloppy payload into header and body lines.
//
// It drops foreign patch envelopes ("*** Begin Patch", "*** Update File:" and
// the like), code fences wrapping the payload, and everything following an end
// sentinel up to the next "*** SM:EDIT" header. Lines inside an "*** SM:AFTER"
// body are content and pass through untouched.
//
// The zero value is not usable; call NewLexer.
type Lexer struct {
	input string
	off   int  // byte offset of the next unconsumed line
	line  int  // number of lines consumed so far
	raw   bool // inside an *** SM:AFTER body, where every line is content
	skip  bool // inside a foreign patch tail following an end sentinel
	tok   Token
}

// NewLexer returns a Lexer over the payload.
func NewLexer(input string) *Lexer {
	return &Lexer{input: input}
}

// Next advances to the next token, reporting whether one exists.
func (l *Lexer) Next() bool {
	for {
		line, ok := l.readLine()
		if !ok {
			return false
		}
		trimmed := strings.TrimSpace(line)
		header, isHeader := parseHeader(line)

		if l.raw && !isHeader {
			l.tok = Token{Kind: TokenText, Line: l.line, Text: line}
			return true
		}
		if !isHeader && trimmed == "***" {
			// A "***" line followed by envelope words ("End of patch") forms one
			// envelope line split across two lines.
			if next, ok := l.peekLine(); ok && isEnvelopeWords(strings.TrimSpace(next)) {
				l.commitPeek()
				trimmed = "*** " + strings.TrimSpace(next)
			}
		}
		if isEnvelopeLine(trimmed) {
			l.skip = isEndSentinel(trimmed)
			continue
		}
		if l.skip {
			if !isHeader || header.Kind != TokenEdit {
				continue
			}
			l.skip = false
		}
		if isHeader {
			header.Line = l.line
			l.raw = header.Kind == TokenAfter
			l.tok = header
			return true
		}
		l.tok = Token{Kind: TokenText, Line: l.line, Text: line}
		return true
	}
}

// Token returns the token produced by the most recent call to Next.
func (l *Lexer) Token() Token { return l.tok }

// readLine consumes and returns the next input line without its line ending.
func (l *Lexer) readLine() (string, bool) {
	if l.off >= len(l.input) {
		return "", false
	}
	start := l.off
	line := l.input[start:]
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
		l.off = start + i + 1
	} else {
		l.off = len(l.input)
	}
	l.line++
	return strings.TrimSuffix(line, "\r"), true
}

// peekLine returns the next input line without consuming it.
func (l *Lexer) peekLine() (string, bool) {
	if l.off >= len(l.input) {
		return "", false
	}
	rest := l.input[l.off:]
	if before, _, ok := strings.Cut(rest, "\n"); ok {
		return strings.TrimSuffix(before, "\r"), true
	}
	return rest, true
}

// commitPeek consumes the line previously returned by peekLine.
func (l *Lexer) commitPeek() { l.readLine() }

// parseHeader recognizes "*** SM:EDIT|FIND|PUT|AFTER" lines. Only SM:EDIT
// accepts an argument; any other header carrying text is body content, so a
// file containing a bare header line cannot be misread as control flow.
func parseHeader(line string) (Token, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "***") || len(trimmed) == 3 {
		return Token{}, false
	}
	if trimmed[3] != ' ' && trimmed[3] != '\t' {
		return Token{}, false
	}
	rest := strings.TrimLeft(trimmed[3:], " \t")
	if len(rest) < 3 || !strings.EqualFold(rest[:3], "SM:") {
		return Token{}, false
	}
	rest = rest[3:]
	name, arg := rest, ""
	if i := strings.IndexAny(rest, " \t"); i >= 0 {
		name, arg = rest[:i], strings.TrimSpace(rest[i:])
	}
	switch {
	case strings.EqualFold(name, "EDIT"):
		path, all, ok := parseEditArg(arg)
		if !ok {
			return Token{}, false
		}
		return Token{Kind: TokenEdit, Text: arg, Path: path, All: all}, true
	case arg != "":
		return Token{}, false
	case strings.EqualFold(name, "FIND"):
		return Token{Kind: TokenFind}, true
	case strings.EqualFold(name, "PUT"):
		return Token{Kind: TokenPut}, true
	case strings.EqualFold(name, "AFTER"):
		return Token{Kind: TokenAfter}, true
	}
	return Token{}, false
}

// parseEditArg splits an SM:EDIT argument into a file path and the "all" flag.
// A quoted path is JSON-decoded so paths may contain spaces or the word "all";
// a malformed quote makes the whole line body text.
func parseEditArg(arg string) (path string, all, ok bool) {
	if strings.EqualFold(arg, "all") {
		return "", true, true
	}
	if i := strings.LastIndexAny(arg, " \t"); i >= 0 && strings.EqualFold(arg[i+1:], "all") {
		all = true
		arg = strings.TrimRight(arg[:i], " \t")
	}
	if arg == "" {
		return "", all, true
	}
	if arg[0] == '"' {
		var decoded string
		if err := json.Unmarshal([]byte(arg), &decoded); err != nil {
			return "", false, false
		}
		if decoded == "" {
			return "", false, false
		}
		return decoded, all, true
	}
	return arg, all, true
}

// isEnvelopeLine reports whether line (trimmed) opens or closes a foreign
// patch envelope, e.g. "*** Begin Patch" or "*** Update File: a.ts".
func isEnvelopeLine(line string) bool {
	if !strings.HasPrefix(line, "***") {
		return false
	}
	rest := strings.TrimLeft(line[3:], " \t")
	for _, prefix := range []string{"abort", "update file:", "add file:", "delete file:"} {
		if hasPrefixFold(rest, prefix) {
			return true
		}
	}
	return isEnvelopeWords(rest)
}

// isEndSentinel reports whether line (trimmed) is a "*** End" sentinel, after
// which payload-foreign text is skipped.
func isEndSentinel(line string) bool {
	if !strings.HasPrefix(line, "***") {
		return false
	}
	rest := strings.TrimLeft(line[3:], " \t")
	if !hasPrefixFold(rest, "end") {
		return false
	}
	rest = rest[len("end"):]
	return rest == "" || !isWordByte(rest[0])
}

// isEnvelopeWords reports whether line (trimmed) reads like an envelope
// fragment, e.g. "Begin of patch" or "End of file".
func isEnvelopeWords(line string) bool {
	rest := line
	switch {
	case hasPrefixFold(rest, "begin"):
		rest = rest[len("begin"):]
	case hasPrefixFold(rest, "end"):
		rest = rest[len("end"):]
	default:
		return false
	}
	rest = strings.TrimLeft(rest, " \t")
	if hasPrefixFold(rest, "of") {
		rest = strings.TrimLeft(rest[len("of"):], " \t")
	}
	noun := rest
	for i := 0; i < len(rest); i++ {
		if !isWordByte(rest[i]) {
			noun = rest[:i]
			break
		}
	}
	switch strings.ToLower(noun) {
	case "patch", "edit", "edits", "file":
		return len(noun) == len(rest) || !isWordByte(rest[len(noun)])
	}
	return false
}

// hasPrefixFold reports whether s starts with prefix under ASCII
// case-insensitive matching without allocating.
func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// isWordByte reports whether b can appear inside an identifier-like word.
func isWordByte(b byte) bool {
	return b == '_' ||
		('a' <= b && b <= 'z') ||
		('A' <= b && b <= 'Z') ||
		('0' <= b && b <= '9')
}
