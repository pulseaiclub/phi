package block

import (
	"strings"

	"github.com/pulseaiclub/xui"
)

// formatCommand breaks long, single-line command chains at shell operators.
// It is display-only; uncertain shell constructs keep their original layout.
func formatCommand(command string, width int, method xui.WidthMethod) string {
	if strings.ContainsAny(command, "\n\r") || xui.StringWidth(command, method) <= width {
		return command
	}
	var breaks []int
	var quote byte
	for i := 0; i < len(command); i++ {
		c := command[i]
		if quote == '\'' {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\\' {
			i++
			continue
		}
		if quote == '"' {
			// Nested substitutions can contain quotes of their own.
			if c == '`' || (c == '$' && i+1 < len(command) && command[i+1] == '(') {
				return command
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			if i > 0 && command[i-1] == '$' {
				return command
			}
			quote = c
		case '(', ')', '{', '}', '[', '`':
			return command
		case '<':
			// Heredocs and here-strings need a shell parser.
			if i+1 < len(command) && command[i+1] == c {
				return command
			}
		case '#':
			if i == 0 || strings.ContainsRune(" \t;&|", rune(command[i-1])) {
				i = len(command)
			}
		case '&', '|':
			if i+1 < len(command) && (command[i+1] == c || (c == '|' && command[i+1] == '&')) {
				i++
			} else if c == '&' || (i > 0 && strings.ContainsRune("<>", rune(command[i-1]))) {
				continue
			}
			breaks = append(breaks, i+1)
		}
	}
	if quote != 0 || len(breaks) == 0 {
		return command
	}
	var formatted strings.Builder
	start := 0
	for _, end := range breaks {
		formatted.WriteString(command[start:end])
		start = end
		for start < len(command) && (command[start] == ' ' || command[start] == '\t') {
			start++
		}
		if start < len(command) {
			formatted.WriteByte('\n')
		}
	}
	formatted.WriteString(command[start:])
	return formatted.String()
}
