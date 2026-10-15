package chat

// ActiveQuestion reports whether the cursor sits in a leading `?` help token.
// Same rules as ActiveSlash: first token only, ends at whitespace.
func ActiveQuestion(value string, cursor int) (query string, start, end int, ok bool) {
	return activeLeadingToken(value, cursor, '?')
}
