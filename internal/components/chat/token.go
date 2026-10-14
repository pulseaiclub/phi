package chat

// activeLeadingToken reports whether the cursor sits in a leading token that
// starts with prefix at byte zero. Only the first token of the composer
// value participates; whitespace ends it and a cursor past that point is not
// active. query is the text after the prefix up to the cursor, and start/end
// are byte offsets to replace on accept (from the prefix through the cursor).
func activeLeadingToken(value string, cursor int, prefix byte) (query string, start, end int, ok bool) {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(value) {
		cursor = len(value)
	}
	if value == "" || value[0] != prefix {
		return "", 0, 0, false
	}
	if cursor < 1 {
		return "", 0, 0, false
	}
	// First whitespace ends the token; picker only while editing it.
	for i := 1; i < len(value); i++ {
		c := value[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			if cursor > i {
				return "", 0, 0, false
			}
			break
		}
	}
	return value[1:cursor], 0, cursor, true
}
