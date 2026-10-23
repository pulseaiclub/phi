package project

import (
	"path/filepath"
	"strings"
)

// stripWin32Namespace removes the extended-path prefix Windows APIs hand back
// when the working directory was set through one (\\?\C:\... or
// \\?\UNC\server\share\...). Left in place, the literal '?' survives the
// slugifier below and Windows refuses to mkdir a name containing it.
func stripWin32Namespace(s string) string {
	if !strings.HasPrefix(s, `\\?\`) {
		return s
	}
	s = s[len(`\\?\`):]
	// \\?\UNC\server\share -> server\share: re-adding a leading \\ would only
	// survive the slugifier as one more dash.
	return strings.TrimPrefix(s, `UNC\`)
}

// ProjectDirName returns a filesystem-safe directory name derived from cwd:
//
//	--<cwd with leading path sep stripped and / \ : replaced by ->--
func ProjectDirName(cwd string) string {
	s := stripWin32Namespace(filepath.Clean(cwd))
	if s == "." {
		return "--unknown--"
	}
	if s != "" && (s[0] == '/' || s[0] == '\\') {
		s = s[1:]
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for _, r := range s {
		switch r {
		case '/', '\\', ':':
			b.WriteByte('-')
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		out = "unknown"
	}
	return "--" + out + "--"
}

// ProjectSessionDir is where jsonl session files for cwd live under baseDir
// (e.g. ~/.phi/session/--Users-me-proj--/).
func ProjectSessionDir(baseDir, cwd string) string {
	return filepath.Join(baseDir, ProjectDirName(cwd))
}
