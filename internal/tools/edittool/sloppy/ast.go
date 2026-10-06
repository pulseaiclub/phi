package sloppy

// PatternTokenKind classifies a PatternToken.
type PatternTokenKind uint8

const (
	// PatternTokenLiteral matches file text verbatim.
	PatternTokenLiteral PatternTokenKind = iota
	// PatternTokenGap is a "…" elision that captures file text.
	PatternTokenGap
)

// PatternToken is one piece of a compiled FIND body.
type PatternToken struct {
	Kind        PatternTokenKind
	Text        string // PatternTokenLiteral: the matched text
	Capture     int    // PatternTokenGap: capture index in gap order
	LineBounded bool   // PatternTokenGap: must stay within one line
	Start, End  int    // byte range within Pattern.Body
}

// Selection is one inline "⟪old│new⟫" directive inside a FIND body. The
// pattern matches Old; the rewrite substitutes New for it.
type Selection struct {
	Old, New   string
	Start, End int // byte range of the directive within Pattern.Body
}

// EdgeGaps records "…" elisions at the pattern edges. They capture nothing and
// are dropped from the token stream.
type EdgeGaps struct {
	Leading  bool
	Trailing bool
}

// Pattern is the compiled FIND body.
type Pattern struct {
	Tokens     []PatternToken
	Selections []Selection
	EdgeGaps   EdgeGaps
	Body       string // cleaned FIND body; Start/End offsets refer to it
}

// RewriteKind classifies a Rewrite.
type RewriteKind uint8

const (
	// RewriteReplace substitutes the PUT body for the whole match. An empty
	// body deletes the match.
	RewriteReplace RewriteKind = iota
	// RewriteInsert keeps the match and inserts the AFTER body after it.
	RewriteInsert
	// RewriteInline carries the rewrite in the pattern's Selections; there is
	// no action header.
	RewriteInline
)

// String returns a human-readable kind name for diagnostics.
func (k RewriteKind) String() string {
	switch k {
	case RewriteReplace:
		return "SM:PUT"
	case RewriteInsert:
		return "SM:AFTER"
	case RewriteInline:
		return "inline selection"
	}
	return "unknown"
}

// Rewrite is the replacement side of an Operation. "…" inside Text replays
// pattern captures; that resolution belongs to the apply stage.
type Rewrite struct {
	Kind RewriteKind
	Text string
}

// Operation is one FIND-anchored edit.
type Operation struct {
	Number  int  // 1-based index within the payload, for diagnostics
	Line    int  // 1-based line of the anchor (FIND header or first body line)
	All     bool // rewrite every match instead of requiring one
	Desired bool // anchor-less: the rewrite asserts desired file content
	Pattern Pattern
	Rewrite Rewrite
}

// Section is one file target and the operations applied to it. Repeated
// "*** SM:EDIT path" headers for the same path coalesce into one section.
type Section struct {
	Path string
	Ops  []Operation
}
