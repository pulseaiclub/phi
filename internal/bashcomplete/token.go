package bashcomplete

import "strings"

// Token is one shell word of a command line: its text as typed and its byte
// offset from the start of the line.
type Token struct {
	Text  string
	Start int
	// Complete reports that the word ended before the cursor: a space or a
	// separator follows it. The word under the cursor is the one being typed,
	// and a completion there extends it rather than starting the next one.
	Complete bool
}

// Tokenize splits a command line into words on unquoted whitespace. Quotes keep
// a word whole — `git add 'my file'` is three words, not four — because a
// completion must never split an argument the shell would keep together. Token
// text keeps its quotes: what gets completed is what the user typed.
//
// The result always holds at least one token, and a line ending in whitespace
// ends with an empty token at len(line): that empty word is the word the cursor
// is typing next, and how the caller knows to complete a new argument rather
// than a prefix of the previous one.
func Tokenize(line string) []Token {
	tokens := splitWords(line)
	if len(tokens) == 0 {
		return []Token{{Text: "", Start: len(line)}}
	}
	if isWordSpace(line[len(line)-1]) || endsInSeparator(line) {
		tokens = append(tokens, Token{Text: "", Start: len(line)})
	}
	// The closing word is the one under the cursor; every other word has been
	// left behind by a separator or a space.
	for i := range tokens {
		tokens[i].Complete = i < len(tokens)-1
	}
	return tokens
}

// Segment returns the part of line the cursor is typing in: a shell command
// list runs several commands, and only the last of them is being completed.
// `cd doc && git sta` completes git's arguments, not doc's.
//
// The offset is where the segment starts in line, so a caller adding token
// offsets still points at the right place in the composer.
func Segment(line string) (segment string, offset int) {
	depth := 0
	var quote byte
	last := 0
	for i := 0; i < len(line); i++ {
		b := line[i]
		switch {
		case quote != 0:
			// A closing quote counts only when it is not escaped: `\'` inside
			// a single-quoted word is part of the word.
			if b == quote && (i == 0 || line[i-1] != '\\') {
				quote = 0
			}
		case b == '\'' || b == '"' || b == '`':
			quote = b
		case b == '(' || b == '{':
			depth++
		case b == ')' || b == '}':
			if depth > 0 {
				depth--
			}
		case depth == 0 && b == ';':
			last = i + 1
		case depth == 0 && b == '|':
			// A pipe hands the next command its turn, whether it is a plain
			// pipe or half of ||.
			if i+1 < len(line) && line[i+1] == '|' {
				i++
			}
			last = i + 1
		case depth == 0 && b == '&':
			// && is the common one; a lone & backgrounds and also starts a new
			// command after it.
			if i+1 < len(line) && line[i+1] == '&' {
				i++
			}
			last = i + 1
		}
	}
	segment = line[last:]
	// A segment usually starts right after its separator; the spaces the user
	// typed there belong to neither side.
	for segment != "" && isWordSpace(segment[0]) {
		segment = segment[1:]
		last++
	}
	return segment, last
}

// WordEnd returns the offset just past the word the cursor sits in, so a caller
// replacing that word takes the text after the cursor with it. `git comm|it -m`
// accepts a completion over `commit`, not over `comm` and then `it`.
func WordEnd(line string, cursor int) int {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(line) {
		cursor = len(line)
	}
	end := cursor
	var quote byte
	// A quote the cursor has not passed yet is still open, even though it sits
	// behind the cursor: `git "a|b" x` with the cursor inside the quotes is one
	// word, and the space it holds does not end it.
	for end > 0 && quote == 0 {
		end--
		if b := line[end]; b == '\'' || b == '"' || b == '`' {
			if end == 0 || line[end-1] != '\\' {
				quote = b
			}
			break
		}
		if isWordSpace(line[end]) || isCommandSeparator(line[end]) {
			break
		}
	}
	end = cursor
	for end < len(line) {
		b := line[end]
		switch {
		case quote != 0:
			if b == quote && (end == 0 || line[end-1] != '\\') {
				quote = 0
			}
		case b == '\'' || b == '"' || b == '`':
			quote = b
		case isWordSpace(b), isCommandSeparator(b):
			return end
		}
		end++
	}
	return end
}

// isCommandSeparator reports whether b closes one command in a list, the way
// the shell reads `&&`, `||`, `;`, `|`, and a lone `&`.
func isCommandSeparator(b byte) bool {
	switch b {
	case ';', '|', '&':
		return true
	}
	return false
}

// endsInSeparator reports whether line closes with a command separator, which
// ends the word before it the way a space does.
func endsInSeparator(line string) bool {
	if line == "" {
		return false
	}
	switch line[len(line)-1] {
	case ';', '|', '&':
		return true
	}
	return false
}

// wrapperCommands are words that run the command after them: completing
// `sudo git sta` has to read git's arguments, not sudo's. The list is the
// small set of launchers a one-liner realistically starts with, not every
// command that can take a command name.
var wrapperCommands = map[string]struct{}{
	"sudo":    {},
	"doas":    {},
	"env":     {},
	"time":    {},
	"nohup":   {},
	"command": {},
	"builtin": {},
	"exec":    {},
	"xargs":   {},
	"nice":    {},
}

// assignment reports whether word is a leading environment assignment such as
// `GOOS=linux`; those configure the command that follows rather than name it.
func assignment(word string) bool {
	name, _, ok := strings.Cut(word, "=")
	if !ok || name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		b := name[i]
		if b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' {
			continue
		}
		return false
	}
	return true
}

// option reports whether word is a flag rather than a word argument.
func option(word string) bool {
	return strings.HasPrefix(word, "-") && word != "-"
}

// CommandWord locates the word that names the command a line runs, skipping
// the launchers and `VAR=value` prefixes in front of it. It returns that
// token's index and true; a line holding only launchers and assignments, or
// nothing at all, has no command word yet.
func CommandWord(tokens []Token) (int, bool) {
	for i, token := range tokens {
		if token.Text == "" {
			return i, false
		}
		if assignment(token.Text) {
			continue
		}
		if _, launcher := wrapperCommands[token.Text]; launcher {
			// The launcher's own flags belong to the launcher: `sudo -u root
			// git` names git, not -u.
			i++
			for i < len(tokens) && option(tokens[i].Text) {
				i++
			}
			if i < len(tokens) && tokens[i].Text != "" {
				return i, true
			}
			return i, false
		}
		return i, true
	}
	return 0, false
}

// splitWords is Tokenize without the trailing-empty-word rule.
func splitWords(line string) []Token {
	var tokens []Token
	start := -1
	var quote byte
	for i := 0; i < len(line); i++ {
		b := line[i]
		switch {
		case quote != 0:
			if b == quote {
				quote = 0
			}
		case b == '\'' || b == '"':
			quote = b
			if start < 0 {
				start = i
			}
		case isWordSpace(b):
			if start >= 0 {
				tokens = append(tokens, Token{Text: line[start:i], Start: start})
				start = -1
			}
		default:
			if start < 0 {
				start = i
			}
		}
	}
	if start >= 0 {
		tokens = append(tokens, Token{Text: line[start:], Start: start})
	}
	return tokens
}

// isWordSpace reports whether b separates shell words. Newlines are included:
// a multi-line "!" command still completes its words.
func isWordSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r':
		return true
	}
	return false
}
