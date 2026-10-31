package prompt

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// contextFileCandidates are checked in order within a single directory;
// the first readable match wins.
var contextFileCandidates = []string{
	"AGENTS.md",
	"AGENTS.MD",
	"CLAUDE.md",
	"CLAUDE.MD",
}

// ContextFile is one loaded project-instruction file.
type ContextFile struct {
	Path    string
	Content string
}

func loadContextFileFromDir(dir string) *ContextFile {
	for _, name := range contextFileCandidates {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		return &ContextFile{Path: path, Content: string(data)}
	}
	return nil
}

// loadProjectContextFiles discovers AGENTS.md / CLAUDE.md in the workspace:
//  1. global agent dir (~/.phi) first
//  2. then every ancestor from filesystem root down to cwd (cwd last)
//
// Each directory contributes at most one file. Paths are deduped.
//
// Slice order is the priority: the last entry is the most specific, so it wins
// a conflict. Callers depend on that order, not on any per-file field.
func loadProjectContextFiles(cwd, agentDir string) []ContextFile {
	var out []ContextFile

	if cwd != "" {
		if abs, err := filepath.Abs(cwd); err == nil {
			cwd = abs
		}
		var ancestors []ContextFile
		seen := make(map[string]struct{})
		dir := cwd
		for {
			if f := loadContextFileFromDir(dir); f != nil {
				if _, ok := seen[f.Path]; !ok {
					seen[f.Path] = struct{}{}
					ancestors = append([]ContextFile{*f}, ancestors...)
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
		out = ancestors
	}

	// The global file ranks below everything in the cwd chain, so prepend it.
	// When the chain already carries it (cwd inside, or equal to, ~/.phi) it is
	// that directory's own file and keeps its more specific position.
	if agentDir != "" {
		if abs, err := filepath.Abs(agentDir); err == nil {
			agentDir = abs
		}
		if f := loadContextFileFromDir(agentDir); f != nil && !containsPath(out, f.Path) {
			out = append([]ContextFile{*f}, out...)
		}
	}

	return out
}

func containsPath(files []ContextFile, path string) bool {
	for _, f := range files {
		if f.Path == path {
			return true
		}
	}
	return false
}

// ProjectContext returns the formatted project instructions for cwd.
func ProjectContext(cwd string) string {
	cwd = resolveDir(cwd)
	return formatProjectContext(loadProjectContextFiles(cwd, phiAgentDir()))
}

func formatProjectContext(files []ContextFile) string {
	if len(files) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("<project_context>\n\n")
	sb.WriteString("Project-specific instructions and guidelines.\n")
	sb.WriteString("Listed lowest to highest priority: on conflict the higher-numbered entry wins.\n\n")
	for i, f := range files {
		sb.WriteString("<project_instructions path=\"")
		sb.WriteString(f.Path)
		sb.WriteString("\" priority=\"")
		sb.WriteString(strconv.Itoa(i + 1))
		sb.WriteString("\">\n")
		sb.WriteString(f.Content)
		if !strings.HasSuffix(f.Content, "\n") {
			sb.WriteByte('\n')
		}
		sb.WriteString("</project_instructions>\n\n")
	}
	sb.WriteString("</project_context>")
	return sb.String()
}

func phiAgentDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".phi")
}
