package project

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// expandHome resolves a leading "~/" (or a bare "~") against the user's home
// directory, so a path copied from the docs works as written. Anything else is
// returned trimmed and untouched; an unresolvable home leaves the input alone
// rather than inventing a path.
func expandHome(path string) string {
	path = strings.TrimSpace(path)
	// Git Bash users write ~\ on Windows; elsewhere a backslash after ~ is
	// just a character in the file name, so only that platform rewrites it.
	if runtime.GOOS == "windows" && strings.HasPrefix(path, `~\`) {
		path = "~/" + path[2:]
	}
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}
