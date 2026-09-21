package shellhist

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// tailBytes is how much of the end of a history file Read decodes: room for
// several hundred entries, while a history that has grown for years is never
// read in full.
const tailBytes = 512 << 10

// Read decodes the tail of the history file at path, oldest first.
//
// A missing or unreadable file is an error, so callers that treat history as
// optional decide for themselves whether silence is right. The commands are not
// deduplicated or limited: ranking does both.
func Read(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	start := max(0, size-tailBytes)
	body := make([]byte, size-start)
	if _, err := file.ReadAt(body, start); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if start > 0 {
		body = trimPartialEntry(body)
	}
	return Parse(body), nil
}

// DefaultPath returns the history file to read: $HISTFILE when it is set,
// otherwise the first conventional file that exists. It returns "" when nothing
// is found, so an absent history is the caller's to report.
func DefaultPath() string {
	if file := strings.TrimSpace(os.Getenv("HISTFILE")); file != "" {
		return file
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, name := range []string{".bash_history", ".zsh_history"} {
		path := filepath.Join(home, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

// trimPartialEntry drops the lines a tail read cut in half. The first newline
// ends the partial entry; in an extended file the continuation lines that
// belong to it carry no ": " marker, so they are skipped as well.
func trimPartialEntry(body []byte) []byte {
	cut := bytes.IndexByte(body, '\n') + 1
	for cut > 0 && cut < len(body) && !startsEntry(body, cut) {
		next := bytes.IndexByte(body[cut:], '\n')
		if next < 0 {
			break
		}
		cut += next + 1
	}
	return body[cut:]
}

// startsEntry reports whether offset at begins a new history entry. A plain
// history file has no marker, so every line boundary starts one; an extended
// file marks each entry with ": " and a digit.
func startsEntry(body []byte, at int) bool {
	extended := len(body) >= 2 && body[0] == ':' && body[1] == ' '
	if !extended {
		return true
	}
	return at+2 < len(body) && body[at] == ':' && body[at+1] == ' ' && isDigit(body[at+2])
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
