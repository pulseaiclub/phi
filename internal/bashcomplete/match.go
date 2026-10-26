package bashcomplete

import (
	"runtime"
	"strings"
)

// hasPrefixFold reports whether candidate starts with word, ignoring case on
// the platforms whose shells ignore it too. A completion is offered only when
// it literally extends what was typed, and on Windows `Git` and `git` are the
// same command, so folding there keeps the rule without hiding real names;
// elsewhere a case-sensitive match stays the shell's own answer.
func hasPrefixFold(candidate, word string) bool {
	if strings.HasPrefix(candidate, word) {
		return true
	}
	if runtime.GOOS != "windows" {
		return false
	}
	return len(candidate) >= len(word) && strings.EqualFold(candidate[:len(word)], word)
}

// foldKey is the comparison key for two words that name the same thing: lower
// case on Windows, the word itself elsewhere.
func foldKey(word string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(word)
	}
	return word
}
