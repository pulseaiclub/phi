package prompt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadContextFileFromDirPrefersAGENTS(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "CLAUDE.md"), "claude rules")
	mustWrite(t, filepath.Join(dir, "AGENTS.md"), "agents rules")

	got := loadContextFileFromDir(dir)
	require.NotNil(t, got, "expected a context file")
	require.Equal(t, "agents rules", got.Content, "prefer AGENTS.md")
}

func TestLoadContextFileFromDirFallsBackToCLAUDE(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "CLAUDE.md"), "claude only")

	got := loadContextFileFromDir(dir)
	require.NotNil(t, got)
	require.Equal(t, "claude only", got.Content)
}

func TestLoadProjectContextFilesOrder(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	mid := filepath.Join(root, "proj")
	cwd := filepath.Join(mid, "nested")
	mustMkdir(t, agentDir)
	mustMkdir(t, cwd)

	mustWrite(t, filepath.Join(agentDir, "AGENTS.md"), "global")
	mustWrite(t, filepath.Join(mid, "AGENTS.md"), "mid")
	mustWrite(t, filepath.Join(cwd, "CLAUDE.md"), "cwd")

	files := loadProjectContextFiles(cwd, agentDir)
	require.Len(t, files, 3)
	want := []string{"global", "mid", "cwd"}
	for i, w := range want {
		require.Equal(t, w, files[i].Content, "files[%d]", i)
	}
}

func TestFormatProjectContext(t *testing.T) {
	got := formatProjectContext([]ContextFile{
		{Path: "/tmp/global/AGENTS.md", Content: "global rules"},
		{Path: "/tmp/proj/AGENTS.md", Content: "use gofmt"},
	})
	require.Contains(t, got, "<project_context>")
	require.Contains(t, got, `<project_instructions path="/tmp/global/AGENTS.md" priority="1">`)
	require.Contains(t, got, `<project_instructions path="/tmp/proj/AGENTS.md" priority="2">`)
	require.Contains(t, got, "use gofmt")
	require.Contains(t, got, "higher-numbered entry wins")
	require.Empty(t, formatProjectContext(nil), "empty files should yield empty string")
}

// TestFormatProjectContextPriorityFollowsOrder pins the priority ladder: the
// cwd's own file outranks every ancestor, and the global file ranks lowest.
func TestFormatProjectContextPriorityFollowsOrder(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	mid := filepath.Join(root, "proj")
	cwd := filepath.Join(mid, "nested")
	mustMkdir(t, agentDir)
	mustMkdir(t, cwd)

	mustWrite(t, filepath.Join(agentDir, "AGENTS.md"), "global")
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "root")
	mustWrite(t, filepath.Join(mid, "AGENTS.md"), "mid")
	mustWrite(t, filepath.Join(cwd, "AGENTS.md"), "cwd")

	files := loadProjectContextFiles(cwd, agentDir)
	require.Len(t, files, 4)
	want := []string{"global", "root", "mid", "cwd"}
	for i, w := range want {
		require.Equal(t, w, files[i].Content, "files[%d]", i)
	}

	got := formatProjectContext(files)
	require.Contains(t, got, `path="`+filepath.Join(agentDir, "AGENTS.md")+`" priority="1"`,
		"global file ranks lowest")
	require.Contains(t, got, `path="`+filepath.Join(cwd, "AGENTS.md")+`" priority="4"`,
		"cwd file ranks highest")
}

// TestLoadProjectContextFilesAgentDirIsAncestor covers cwd nested inside the
// global dir: that file is one of cwd's ancestors, so it must appear once at
// the lower rank rather than being both prepended as global and re-added.
func TestLoadProjectContextFilesAgentDirIsAncestor(t *testing.T) {
	agentDir := t.TempDir()
	cwd := filepath.Join(agentDir, "nested")
	mustMkdir(t, cwd)
	mustWrite(t, filepath.Join(agentDir, "AGENTS.md"), "global")
	mustWrite(t, filepath.Join(cwd, "AGENTS.md"), "local")

	files := loadProjectContextFiles(cwd, agentDir)
	require.Len(t, files, 2, "the shared file must not be listed twice")
	require.Equal(t, "global", files[0].Content)
	require.Equal(t, "local", files[1].Content)

	got := formatProjectContext(files)
	require.Contains(t, got, `path="`+filepath.Join(agentDir, "AGENTS.md")+`" priority="1"`)
	require.Contains(t, got, `path="`+filepath.Join(cwd, "AGENTS.md")+`" priority="2"`,
		"the cwd's own file must outrank the global dir's")
}

// TestLoadProjectContextFilesDedupesAgentDirKeepsLocalRank covers the global dir
// equal to cwd: one file, ranked from its position, never the zero value.
func TestLoadProjectContextFilesDedupesAgentDirKeepsLocalRank(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "AGENTS.md"), "once")

	files := loadProjectContextFiles(dir, dir)
	require.Len(t, files, 1)
	require.Equal(t, "once", files[0].Content)

	got := formatProjectContext(files)
	require.Contains(t, got, `priority="1"`)
	require.NotContains(t, got, `priority="0"`, "priority is 1-based")
}

func TestBuildIncludesContext(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "AGENTS.md"), "always use tabs")

	ctx := formatProjectContext(loadProjectContextFiles(dir, t.TempDir()))
	require.Contains(t, ctx, "always use tabs")
}

func TestProjectContextUsesCwd(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "AGENTS.md"), "always use tabs")

	require.Contains(t, ProjectContext(dir), "always use tabs")
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(path, 0o755))
}
