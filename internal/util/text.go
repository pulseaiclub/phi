package util

import (
	"strings"
	"unicode/utf8"
)

// NormalizeLF converts CRLF and lone CR line endings to LF.
func NormalizeLF(text string) string {
	text = ReplaceAll(text, "\r\n", "\n")
	return ReplaceAll(text, "\r", "\n")
}

// ReplaceAll returns a copy of the string s with all
// non-overlapping instances of old replaced by new.
// If old is empty, it matches at the beginning of the string
// and after each UTF-8 sequence, yielding up to k+1 replacements
// for a k-rune string.
func ReplaceAll(s, old, new string) string {
	return strings.ReplaceAll(s, old, new)
}

// TruncateBytes retains at most n bytes of s, appending suffix only when truncated.
// The suffix is outside the budget. n must be nonnegative; UTF-8 may be split.
func TruncateBytes(s string, n int, suffix string) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + suffix
}

// TruncateRunes retains at most n runes of s, appending suffix only when truncated.
// The suffix is outside the budget. n must be nonnegative. On truncation,
// invalid UTF-8 in the retained prefix is replaced with the Unicode replacement character.
func TruncateRunes(s string, n int, suffix string) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + suffix
}
