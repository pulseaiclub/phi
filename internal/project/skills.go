package project

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// agentsDirName is the Agent Skills directory. Other harnesses already write
// <root>/.agents/skills, so phi reads it alongside its own skills directory.
const agentsDirName = ".agents"

// resolveSkillPaths returns the ordered roots a session loads SKILL.md files
// from: the configured directory first (already "~"-expanded, and defaulted by
// the caller), then ~/.agents/skills, then .agents/skills from cwd up to its
// enclosing git root. The first root wins when two of them hold a skill with
// the same name, so the user's own directory keeps priority.
func resolveSkillPaths(configured string, global GlobalLayout, cwd string) []string {
	roots := make([]string, 0, 6)
	// home is the parent of ~/.phi; a literal ~ never reaches this point.
	userAgents := filepath.Join(filepath.Dir(global.Root()), agentsDirName, "skills")

	for _, dir := range append([]string{configured, userAgents}, ancestorAgentsSkillDirs(cwd)...) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		dir = filepath.Clean(dir)
		if !slices.Contains(roots, dir) {
			roots = append(roots, dir)
		}
	}
	return roots
}

// ancestorAgentsSkillDirs collects <dir>/.agents/skills from cwd upward,
// stopping at the directory that holds .git so a repository can ship project
// skills without also inheriting an unrelated ancestor's. Outside a repository
// the walk ends at the filesystem root; the extra roots are harmless, since a
// directory that does not exist simply has no skills.
func ancestorAgentsSkillDirs(cwd string) []string {
	if strings.TrimSpace(cwd) == "" {
		return nil
	}
	dirs := []string{}
	dir := filepath.Clean(cwd)
	for {
		dirs = append(dirs, filepath.Join(dir, agentsDirName, "skills"))
		if isGitRoot(dir) {
			return dirs
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dirs
		}
		dir = parent
	}
}

func isGitRoot(dir string) bool {
	// A .git directory is an ordinary repository; a .git file is a worktree or
	// submodule. Both mark the top of the project.
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}
