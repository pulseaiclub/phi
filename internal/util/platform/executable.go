// Package platform contains small operating-system adaptations shared by callers.
package platform

import (
	"os"
	"path/filepath"
	"runtime"
)

// EnsureExecutable makes a file executable on platforms that use Unix mode bits.
func EnsureExecutable(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	return os.Chmod(path, 0o755)
}

// ExecutableName returns the platform filename for a program with the given base name.
func ExecutableName(name string) string {
	if runtime.GOOS == "windows" && filepath.Ext(name) == "" {
		return name + ".exe"
	}
	return name
}

// ExecutableCandidates returns names to probe for a program in a fixed directory.
func ExecutableCandidates(path string) []string {
	name := ExecutableName(path)
	if name != path {
		return []string{name, path}
	}
	return []string{path}
}
