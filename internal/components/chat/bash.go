package chat

// ActiveBash reports whether the cursor sits in the command text of a "!" shell
// line. start/end are byte offsets into value for the range to replace on
// accept — the command after "!" up to the cursor — and query is that text.
//
// "!" mode owns the whole line, so the command may be empty (the user has typed
// only the bang) and may span several lines; how short is too short to complete
// is the predictor's call, not this one's.
func ActiveBash(value string, cursor int) (query string, start, end int, ok bool) {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(value) {
		cursor = len(value)
	}
	// Leading whitespace is allowed before the bang, matching how the submitter
	// reads the prefix: it trims the line before looking for it.
	bang := 0
	for bang < len(value) && isBashSpace(value[bang]) {
		bang++
	}
	if bang >= len(value) || value[bang] != '!' {
		return "", 0, 0, false
	}
	start = bang + 1
	if cursor < start {
		return "", 0, 0, false
	}
	return value[start:cursor], start, cursor, true
}

// isBashSpace reports whether b is whitespace the submitter would trim before
// reading a leading bang. TrimSpace also covers vertical tab and form feed,
// which cannot begin a line in practice.
func isBashSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r':
		return true
	}
	return false
}
