package sloppy

// TargetPaths lists the file paths a sloppy payload edits, in first-seen
// order. It lexes only, so a payload with body errors still reports its
// targets and the edit stage can produce the full diagnostic.
func TargetPaths(payload string) []string {
	lexer := NewLexer(trimOuterFence(payload))
	var (
		seen  = make(map[string]bool)
		paths []string
	)
	for lexer.Next() {
		token := lexer.Token()
		if token.Kind != TokenEdit || token.Path == "" || seen[token.Path] {
			continue
		}
		seen[token.Path] = true
		paths = append(paths, token.Path)
	}
	return paths
}
