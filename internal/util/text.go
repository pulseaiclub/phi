package util

import (
	"strings"
	"unicode/utf8"
)

// NormalizeLF converts CRLF and lone CR line endings to LF.
func NormalizeLF(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
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
