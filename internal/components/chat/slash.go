package chat

// ActiveSlash reports whether the cursor sits in a leading `/command` token.
// Only the first token of the composer value participates (slash commands are
// whole-line). query is the text after '/' up to the cursor.
// start/end are byte offsets to replace on accept (from '/' through cursor).
func ActiveSlash(value string, cursor int) (query string, start, end int, ok bool) {
	return activeLeadingToken(value, cursor, '/')
}
