package shellhist

import (
	"slices"
	"strings"
	"unicode"
)

// metaByte is zsh's Meta escape: it precedes a byte whose value was XORed with
// 0x20, so bytes at or above 0x80 survive a file read back as text.
const metaByte = 0x83

// Parse decodes a shell history file body into commands, oldest first.
//
// It reads what the shells actually write: zsh's extended format
// (`: <epoch>:<duration>;<command>`), the plain one-line-per-entry format both
// shells use without EXTENDED_HISTORY, and bash's `#<epoch>` marker lines. zsh
// escapes bytes at or above 0x80 with metaByte, and writes an embedded newline
// as a trailing backslash on the line.
//
// Duplicates are kept: a command run twice is two entries, and collapsing them
// belongs to whatever is ranking the result.
func Parse(body []byte) []string {
	text := strings.ToValidUTF8(string(unmetafy(body)), "\uFFFD")
	lines := strings.Split(text, "\n")
	commands := make([]string, 0, len(lines))
	for index := 0; index < len(lines); index++ {
		line := strings.TrimSuffix(lines[index], "\r")
		// A trailing backslash continues the entry on the next line. zsh writes
		// an embedded newline that way, so a command that itself ends a line
		// with a backslash appears as two of them, and joining strips one.
		for strings.HasSuffix(line, `\`) && index+1 < len(lines) {
			index++
			line = strings.TrimSuffix(line, `\`) + "\n" + strings.TrimSuffix(lines[index], "\r")
		}
		if strings.TrimSpace(line) == "" || isTimestamp(line) {
			continue
		}
		command := strings.TrimRightFunc(stripExtended(line), unicode.IsSpace)
		if command == "" {
			continue
		}
		commands = append(commands, command)
	}
	return commands
}

// stripExtended removes zsh's ": <epoch>:<duration>;" prefix.
//
// A line without the prefix, or with a prefix whose fields are not numbers, is
// returned unchanged: only the exact extended shape may lose its head, or a
// command that merely starts with a colon would be truncated.
func stripExtended(line string) string {
	rest, ok := strings.CutPrefix(line, ": ")
	if !ok {
		return line
	}
	epoch, rest, ok := strings.Cut(rest, ":")
	if !ok || !isDigits(epoch) {
		return line
	}
	duration, command, ok := strings.Cut(rest, ";")
	if !ok || !isDigits(duration) {
		return line
	}
	return command
}

// isTimestamp reports whether line is one of bash's `#<epoch>` markers. Bash
// writes one before each entry when HISTTIMEFORMAT is set, and its own reader
// takes the line for a timestamp rather than a command.
func isTimestamp(line string) bool {
	rest, ok := strings.CutPrefix(line, "#")
	return ok && isDigits(rest)
}

// unmetafy reverses zsh's Meta escaping. A body without the escape byte is
// returned as it came, so the common case costs one scan.
func unmetafy(body []byte) []byte {
	if !slices.Contains(body, metaByte) {
		return body
	}
	out := make([]byte, 0, len(body))
	for index := 0; index < len(body); index++ {
		if body[index] == metaByte && index+1 < len(body) {
			index++
			out = append(out, body[index]^0x20)
			continue
		}
		out = append(out, body[index])
	}
	return out
}

func isDigits(text string) bool {
	if text == "" {
		return false
	}
	for _, char := range text {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
