package edittool

import (
	"strings"
	"unicode/utf8"
)

// normText is source text mapped into the matching space: whitespace is
// dropped and typographic characters fold to their ASCII spellings, so pattern
// and file may differ in indentation, spacing, and punctuation style. starts
// and ends map every normalized byte back to the source byte range it came
// from, so matched spans and captures can be cut out of the original text.
type normText struct {
	text   string
	starts []int // starts[i] is the source byte offset where normalized byte i begins
	ends   []int // ends[i] is the source byte offset where normalized byte i ends
}

// normalizeText maps source into the matching space in one pass.
func normalizeText(source string) normText {
	var (
		text   strings.Builder
		starts []int
		ends   []int
	)
	for offset := 0; offset < len(source); {
		r, width := utf8.DecodeRuneInString(source[offset:])
		end := offset + width
		if isFoldedWhitespace(r) {
			offset = end
			continue
		}
		replacement := string(r)
		if r >= utf8.RuneSelf {
			replacement = foldRune(r)
		}
		text.WriteString(replacement)
		for range len(replacement) {
			starts = append(starts, offset)
			ends = append(ends, end)
		}
		offset = end
	}
	return normText{text: text.String(), starts: starts, ends: ends}
}

// isFoldedWhitespace reports the characters the matching space erases: ASCII
// whitespace plus the Unicode whitespace JavaScript's trim removes.
func isFoldedWhitespace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ',
		0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// foldRune maps one non-ASCII character into its ASCII spelling: dash, quote,
// and not-equal variants fold to ASCII, zero-width characters vanish, and
// everything else is kept verbatim.
func foldRune(r rune) string {
	switch {
	case r >= 0x2010 && r <= 0x2015, r == 0x2212:
		return "-"
	case r >= 0x2018 && r <= 0x201B:
		return "'"
	case r >= 0x201C && r <= 0x201F:
		return "\""
	case r == 0x2260:
		return "!="
	case r == 0xBD:
		return "1/2"
	case r >= 0x200B && r <= 0x200D, r == 0x2016, r == 0x2017:
		return ""
	default:
		return string(r)
	}
}

// sourceStart maps a normalized byte offset to the source byte where that
// byte's source character starts.
func (n normText) sourceStart(offset, fallback int) int {
	if offset < len(n.starts) {
		return n.starts[offset]
	}
	return fallback
}

// sourceEnd maps a normalized byte offset to the source byte where the
// previous normalized byte's source character ends.
func (n normText) sourceEnd(offset, fallback int) int {
	if offset == 0 {
		return 0
	}
	if offset-1 < len(n.ends) {
		return n.ends[offset-1]
	}
	return fallback
}

// levenshteinDistance is the edit distance over Unicode characters, with the
// shared prefix and suffix trimmed before the rolling-array dynamic program.
func levenshteinDistance(left, right string) int {
	a, b := []rune(left), []rune(right)
	if len(a) > len(b) {
		a, b = b, a
	}
	for len(a) > 0 && len(b) > 0 && a[0] == b[0] {
		a, b = a[1:], b[1:]
	}
	for len(a) > 0 && len(b) > 0 && a[len(a)-1] == b[len(b)-1] {
		a, b = a[:len(a)-1], b[:len(b)-1]
	}
	if len(a) == 0 {
		return len(b)
	}
	previous := make([]int, len(a)+1)
	current := make([]int, len(a)+1)
	for i := range previous {
		previous[i] = i
	}
	for j := 1; j <= len(b); j++ {
		current[0] = j
		for i := 1; i <= len(a); i++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[i] = min(min(current[i-1]+1, previous[i]+1), previous[i-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(a)]
}
