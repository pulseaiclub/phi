package bashcomplete

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// pathCacheTTL bounds how long one PATH scan is trusted. PATH rarely changes
// mid-session, and a scan walks every PATH directory, so it is cheaper to reuse
// a slightly stale list than to rescan on every keystroke.
const pathCacheTTL = time.Minute

var (
	pathCacheMu sync.Mutex
	pathCache   struct {
		path  string
		ext   string
		names []string
		at    time.Time
	}
)

// builtins are the shell words a user can reasonably type in a "!" one-liner
// that no PATH directory has to contain.
var builtins = []string{"cd", "echo", "env", "exit", "export", "pwd", "source", "which"}

// PathCommands returns the executable names found on PATH plus the shell
// builtins, sorted, as they would be typed. The result is cached: the
// completer runs off the UI goroutine, but a completion should not pay for a
// walk of every PATH directory on every keystroke.
func PathCommands() []string {
	pathEnv := os.Getenv("PATH")
	extEnv := os.Getenv("PATHEXT")
	pathCacheMu.Lock()
	defer pathCacheMu.Unlock()
	if pathCache.names != nil && pathCache.path == pathEnv && pathCache.ext == extEnv &&
		time.Since(pathCache.at) < pathCacheTTL {
		return pathCache.names
	}
	names := scanPath(pathEnv)
	names = append(names, builtins...)
	sort.Strings(names)
	names = dedupeNames(names)
	pathCache.path, pathCache.ext, pathCache.names, pathCache.at = pathEnv, extEnv, names, time.Now()
	return names
}

// scanPath collects the names that name an executable in some PATH directory:
// the mode says so on unix, the extension says so on Windows. Empty entries and
// unreadable directories are skipped — a completion source must never fail the
// completion.
func scanPath(pathEnv string) []string {
	var names []string
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if isExecutable(entry) {
				names = append(names, entry.Name())
			}
		}
	}
	return names
}

// isExecutable reports whether a directory entry names something runnable.
func isExecutable(entry os.DirEntry) bool {
	if entry.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".exe", ".com", ".bat", ".cmd":
			return true
		}
		return false
	}
	info, err := entry.Info()
	if err != nil {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}

// dedupeNames drops repeated names, case-insensitively on Windows where
// Git.EXE and git.exe are the same command. First occurrence wins; the list is
// sorted, so that is also the lexicographically smallest spelling.
func dedupeNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	out := names[:0]
	for _, name := range names {
		key := name
		if runtime.GOOS == "windows" {
			key = strings.ToLower(name)
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	return out
}
