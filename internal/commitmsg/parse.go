package commitmsg

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Intentionally recognize a small grammar, not a shell command language.
var commandPattern = regexp.MustCompile(`^[ \t]*git[ \t]+commit[ \t]+(?:(-a|--all)[ \t]+)?(-m|--message|-am)(.*)$`)

type entry struct {
	prefix string
	quote  byte
	all    bool
	space  bool
}

func parse(typed string) (entry, bool) {
	match := commandPattern.FindStringSubmatch(typed)
	if match == nil {
		return entry{}, false
	}
	e := entry{all: match[1] != "" || match[2] == "-am"}
	if match[1] != "" && match[2] == "-am" {
		return entry{}, false
	}
	rest := match[3]
	if match[2] == "--message" && strings.HasPrefix(rest, "=") {
		rest = rest[1:]
	} else if rest == "" {
		e.space = true
	} else if rest[0] == ' ' || rest[0] == '\t' {
		rest = strings.TrimLeft(rest, " \t")
	} else {
		return entry{}, false
	}
	if rest == "" {
		return e, true
	}
	e.quote = rest[0]
	if e.quote != '\'' && e.quote != '"' {
		return entry{}, false
	}
	var prefix strings.Builder
	for i := 1; i < len(rest); i++ {
		c := rest[i]
		if c == e.quote || c < ' ' || c == 127 {
			return entry{}, false
		}
		if e.quote == '"' {
			if c == '$' || c == '`' || c == '!' {
				return entry{}, false
			}
			if c == '\\' {
				if i+1 == len(rest) {
					return entry{}, false
				}
				next := rest[i+1]
				if strings.ContainsRune("$`\"\\", rune(next)) {
					i++
					c = next
				}
			}
		}
		prefix.WriteByte(c)
	}
	e.prefix = prefix.String()
	return e, utf8.ValidString(e.prefix)
}

func (e entry) extend(typed, subject string) string {
	suffix := strings.TrimPrefix(subject, e.prefix)
	if e.space {
		typed += " "
	}
	if e.quote == 0 {
		typed += "'"
	}
	if e.quote == '"' {
		// Single-quote exclamation marks to avoid interactive history expansion.
		escape := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "$", "\\$", "`", "\\`", "!", "\"'!'\"")
		return typed + escape.Replace(suffix) + "\""
	}
	return typed + strings.ReplaceAll(suffix, "'", "'\\''") + "'"
}

func validSubject(subject, prefix string) bool {
	if subject == "" || len(subject) > 240 || !utf8.ValidString(subject) ||
		strings.TrimSpace(subject) != subject || !strings.HasPrefix(subject, prefix) {
		return false
	}
	for _, r := range subject {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return false
		}
	}
	if len(subject) >= 2 && ((subject[0] == '\'' && subject[len(subject)-1] == '\'') ||
		(subject[0] == '"' && subject[len(subject)-1] == '"')) {
		return false
	}
	lower := strings.ToLower(subject)
	return !strings.Contains(subject, "```") && !strings.HasPrefix(subject, "~~~") &&
		!strings.HasPrefix(lower, "git ") && !strings.HasPrefix(lower, "$ ") &&
		!strings.HasPrefix(lower, "subject:") && !strings.HasPrefix(lower, "commit message:")
}
