package permission

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func defaultSensitivePaths() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return []string{
			"/.ssh",
			"/.phi/config.yaml",
			"/etc/shadow",
			"/etc/passwd",
		}
	}
	return []string{
		filepath.Join(home, ".ssh"),
		filepath.Join(home, ".phi", "config.yaml"),
		filepath.Join(home, ".aws", "credentials"),
		filepath.Join(home, ".gnupg"),
		"/etc/shadow",
	}
}

// WorkspaceRoot returns the git-root workspace, or cwd if no .git is found.
func WorkspaceRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	start := dir
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return start
}

// IsInternalURL reports whether path is an internal URL (mcp://…) rather than
// a filesystem path. Internal URLs must never be cwd-joined or workspace-
// checked: they name remote resources, not files.
func IsInternalURL(path string) bool {
	return strings.HasPrefix(path, "mcp://")
}

// AbsCleanAt resolves path to an absolute cleaned path against cwd (no symlink resolve).
// Empty cwd uses the process working directory.
func AbsCleanAt(path, cwd string) (string, error) {
	if path == "" {
		path = "."
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	if cwd == "" {
		dir, err := os.Getwd()
		if err != nil {
			return "", err
		}
		cwd = dir
	} else if !filepath.IsAbs(cwd) {
		abs, err := filepath.Abs(cwd)
		if err != nil {
			return "", err
		}
		cwd = abs
	} else {
		cwd = filepath.Clean(cwd)
	}
	return filepath.Clean(filepath.Join(cwd, path)), nil
}

// realPath resolves existing ancestors before appending a not-yet-created suffix.
// Dangling links and other resolution errors fail closed.
// This is a policy check, not protection against concurrent symlink replacement.
func realPath(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	var suffix string
	if os.IsNotExist(err) {
		for {
			// Peel only absent entries; an existing dangling link must fail.
			_, statErr := os.Lstat(path)
			if !os.IsNotExist(statErr) {
				// Let EvalSymlinks handle chains beyond the kernel's lookup limit.
				resolved, err = filepath.EvalSymlinks(path)
				break
			}
			parent := filepath.Dir(path)
			if parent == path {
				return "", err
			}
			suffix = filepath.Join(filepath.Base(path), suffix)
			path = parent
		}
	}
	if err != nil {
		return "", err
	}
	if suffix != "" {
		info, err := os.Stat(resolved)
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			return "", fmt.Errorf("resolve %q: not a directory", resolved)
		}
	}
	if runtime.GOOS == "windows" {
		if err := rejectIrregularAncestors(resolved); err != nil {
			return "", err
		}
	}
	return filepath.Join(resolved, suffix), nil
}

// EvalSymlinks can leave Windows junctions and other reparse points unresolved.
func rejectIrregularAncestors(path string) error {
	for {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeIrregular != 0 {
			return fmt.Errorf("resolve %q: unsupported file type", path)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return nil
		}
		path = parent
	}
}

// InWorkspace reports whether absPath is inside workspace (or equal to it).
func InWorkspace(absPath, workspace string) bool {
	if workspace == "" || absPath == "" {
		return false
	}
	absPath = filepath.Clean(absPath)
	workspace = filepath.Clean(workspace)
	if absPath == workspace {
		return true
	}
	rel, err := filepath.Rel(workspace, absPath)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// SensitivePaths is the default deny prefix list for reads and writes. Exposed
// so a UI that reads files itself (the code viewer) answers to the same list the
// gate enforces, instead of quietly being the one hole in it.
func SensitivePaths() []string { return defaultSensitivePaths() }

// IsSensitivePath reports whether absPath matches a sensitive prefix.
func IsSensitivePath(absPath string, prefixes []string) bool {
	absPath = sensitiveSpelling(absPath)
	for _, p := range prefixes {
		p = sensitiveSpelling(p)
		if absPath == p {
			return true
		}
		if strings.HasPrefix(absPath, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// Deny case aliases conservatively on platforms commonly using insensitive
// volumes. This can also deny distinct case variants on case-sensitive volumes.
func sensitiveSpelling(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

// bashEligibleForAllowlist reports whether cmd is a single simple command that
// may be auto-allowed. Chaining, pipes, substitutions, and overwrite redirects
// force Ask/Deny evaluation instead of prefix allowlist matches.
func bashEligibleForAllowlist(cmd string) bool {
	return !hasBashControlSyntax(cmd)
}

func hasBashControlSyntax(cmd string) bool {
	inSingle, inDouble, escaped := false, false, false
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && !inSingle {
			escaped = true
			continue
		}
		if c == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if c == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if inSingle || inDouble {
			continue
		}
		switch c {
		case '\n', '\r', ';', '|', '`':
			return true
		case '&':
			if i+1 < len(cmd) && cmd[i+1] == '&' {
				return true
			}
			// background `&` also chains intent; treat as control
			return true
		case '>':
			// Allow only >/dev/null and N>/dev/null (stderr noise); other
			// redirects can overwrite files and must not use the allowlist.
			if isDevNullRedirect(cmd, i) {
				continue
			}
			return true
		case '$':
			if i+1 < len(cmd) && (cmd[i+1] == '(' || cmd[i+1] == '{') {
				return true
			}
		}
	}
	return false
}

// isDevNullRedirect reports whether cmd[i] is the '>' of a >/dev/null redirect
// (optionally preceded by a FD digit, optionally >>).
func isDevNullRedirect(cmd string, gt int) bool {
	j := gt
	if j+1 < len(cmd) && cmd[j+1] == '>' {
		j++
	}
	rest := strings.TrimSpace(cmd[j+1:])
	return strings.HasPrefix(rest, "/dev/null")
}
