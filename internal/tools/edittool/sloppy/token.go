package sloppy

// TokenKind classifies a Token.
type TokenKind uint8

const (
	// TokenText is a raw body line.
	TokenText TokenKind = iota
	// TokenEdit is a "*** SM:EDIT" header.
	TokenEdit
	// TokenFind is a "*** SM:FIND" header.
	TokenFind
	// TokenPut is a "*** SM:PUT" header.
	TokenPut
	// TokenAfter is a "*** SM:AFTER" header.
	TokenAfter
)

// String returns a human-readable kind name for diagnostics.
func (k TokenKind) String() string {
	switch k {
	case TokenText:
		return "text"
	case TokenEdit:
		return "SM:EDIT"
	case TokenFind:
		return "SM:FIND"
	case TokenPut:
		return "SM:PUT"
	case TokenAfter:
		return "SM:AFTER"
	}
	return "unknown"
}

// Token is one logical payload line produced by the Lexer.
type Token struct {
	Kind TokenKind
	Line int    // 1-based line number within the input
	Text string // TokenText: the raw line; TokenEdit: the raw argument
	Path string // TokenEdit: file target; empty continues the current section
	All  bool   // TokenEdit: rewrite every match
}
