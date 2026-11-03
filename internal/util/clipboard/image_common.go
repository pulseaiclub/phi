package clipboard

import (
	"strings"
)

// splitLines splits on LF (normalizing CRLF) and drops empty lines.
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")

	if s == "" {
		return nil
	}
	raw := strings.Split(s, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
