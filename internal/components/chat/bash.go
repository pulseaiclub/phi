package chat

// ActiveBash reports whether the cursor sits in the command text of a "!" shell
// line. start is the byte offset of the command text — just after the bang — so
// an accept can replace the command without touching the bang or the whitespace
// before it, and query is the text between start and the cursor.
//
// "!" mode owns the whole line, so the command may be empty (the user has typed
// only the bang) and may span several lines; how short is too short to complete
// is the predictor's call, not this one's.
func ActiveBash(value string, cursor int) (query string, start int, ok bool) {
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
		return "", 0, false
	}
	start = bang + 1
	if cursor < start {
		return "", 0, false
	}
	return value[start:cursor], start, true
}

// ActiveBashMode is ActiveBash for a composer that may be in shell mode. Shell
// mode has no bang to find: the whole buffer is the command text, starting at
// 0, and even an empty buffer is an (empty) command so the completer can settle
// while the user has not typed anything yet.
func ActiveBashMode(value string, cursor int, shellMode bool) (query string, start int, ok bool) {
	if !shellMode {
		return ActiveBash(value, cursor)
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(value) {
		cursor = len(value)
	}
	return value[:cursor], 0, true
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
