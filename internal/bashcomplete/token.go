package bashcomplete

// Token is one shell word of a command line: its text as typed and its byte
// offset from the start of the line.
type Token struct {
	Text  string
	Start int
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
	if isWordSpace(line[len(line)-1]) {
		tokens = append(tokens, Token{Text: "", Start: len(line)})
	}
	return tokens
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
