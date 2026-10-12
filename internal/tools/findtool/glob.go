package findtool

import (
	"errors"
	"regexp"
	"strings"
)

// globToRegex converts a find glob into an anchored regex for fd.
//
// fd's own --glob matching is broken on Windows: globset matches the raw
// backslash path against slash-only pattern semantics, so any pattern with a
// literal path component or a literal file name silently matches nothing.
// Translating the glob here gives one code path for every OS and accepts both
// separators in the candidate path. Backslash escapes are not supported; both
// / and \ in a pattern act as path separators.
//
// Supported: * / ** / ? / [abc] / [!abc] / {a,b}.
func globToRegex(pattern string) (string, error) {
	pattern = strings.ReplaceAll(pattern, `\`, "/")
	var b strings.Builder
	b.WriteByte('^')
	if err := writeGlob(&b, pattern); err != nil {
		return "", err
	}
	b.WriteByte('$')
	return b.String(), nil
}

func writeGlob(b *strings.Builder, s string) error {
	for i := 0; i < len(s); {
		switch s[i] {
		case '*':
			j := i
			for j < len(s) && s[j] == '*' {
				j++
			}
			// A ** spanning a whole path component crosses separators and
			// matches zero directories, so "a/**/b" also matches "a/b".
			if j-i >= 2 && (i == 0 || s[i-1] == '/') && (j == len(s) || s[j] == '/') {
				if j < len(s) {
					b.WriteString(`(?:.*[/\\])?`)
					i = j + 1
					continue
				}
				b.WriteString(`.*`)
				i = j
				continue
			}
			b.WriteString(`[^/\\]*`)
			i = j
		case '?':
			b.WriteString(`[^/\\]`)
			i++
		case '/':
			b.WriteString(`[/\\]`)
			i++
		case '[':
			class, next, err := parseClass(s, i)
			if err != nil {
				return err
			}
			b.WriteString(class)
			i = next
		case '{':
			parts, next, err := splitBrace(s, i)
			if err != nil {
				return err
			}
			b.WriteString(`(?:`)
			for k, part := range parts {
				if k > 0 {
					b.WriteByte('|')
				}
				if err := writeGlob(b, part); err != nil {
					return err
				}
			}
			b.WriteByte(')')
			i = next
		default:
			b.WriteString(regexp.QuoteMeta(string(s[i])))
			i++
		}
	}
	return nil
}

// parseClass converts a glob character class starting at s[i] == '[' into a
// regex class. Returns the class, the index after the closing bracket, or an
// error for an unclosed class.
func parseClass(s string, i int) (string, int, error) {
	j := i + 1
	negate := false
	if j < len(s) && s[j] == '!' {
		negate = true
		j++
	}
	if j < len(s) && s[j] == ']' {
		j++ // a leading ] is a literal in globs
	}
	for j < len(s) && s[j] != ']' {
		j++
	}
	if j >= len(s) {
		return "", 0, errors.New("unclosed character class")
	}
	body := s[i+1 : j]
	if negate {
		body = body[1:]
	}
	var b strings.Builder
	b.WriteByte('[')
	if negate {
		b.WriteByte('^')
	}
	for k := 0; k < len(body); k++ {
		switch body[k] {
		case ']', '^', '&', '\\':
			// Escape chars that are special inside a regex class (Rust's
			// class intersection would eat a bare &).
			b.WriteByte('\\')
		}
		b.WriteByte(body[k])
	}
	b.WriteByte(']')
	return b.String(), j + 1, nil
}

// splitBrace splits a brace group starting at s[i] == '{' into its top-level
// comma alternatives. Returns the parts, the index after the closing brace,
// or an error for unbalanced braces.
func splitBrace(s string, i int) ([]string, int, error) {
	depth := 0
	start := i + 1
	var parts []string
	for j := i; j < len(s); j++ {
		switch s[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				parts = append(parts, s[start:j])
				return parts, j + 1, nil
			}
		case ',':
			if depth == 1 {
				parts = append(parts, s[start:j])
				start = j + 1
			}
		}
	}
	return nil, 0, errors.New("unbalanced braces")
}
