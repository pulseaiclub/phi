package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildAgentsEnabledToggle(t *testing.T) {
	with := Build("", nil, true, 4, nil)
	without := Build("", nil, false, 0, nil)

	require.Contains(t, with, "agent_spawn")
	require.Contains(t, with, "Sub-agents:")
	require.Contains(t, with, "At most 4 sub-agents run concurrently")
	require.NotContains(t, without, "agent_spawn")
	require.NotContains(t, without, "sub-agents run concurrently")
	require.Contains(t, without, "`find` / `grep` / `ls` yourself")
}

func TestBuildEditHashCopyIsUnambiguous(t *testing.T) {
	got := Build("", nil, false, 0, nil)
	require.Contains(t, got, "*** SM:EDIT path")
	require.Contains(t, got, "*** SM:FIND")
	require.NotContains(t, got, "Known path or exact symbol")
	require.NotContains(t, got, "creates a new file only")
	require.NotContains(t, got, "fails if it already exists")
	require.Contains(t, got, "`write` creates or overwrites")
	require.Contains(t, got, "Prefer cwd-relative paths")
}

// TestBuildDeclaresInstructionPriority pins PP-02: the prompt states a
// resolution ladder, keeps the user's instruction below the permission gate,
// and points at the numbered entries in <project_context>.
func TestBuildDeclaresInstructionPriority(t *testing.T) {
	got := Build("", nil, false, 0, nil)

	require.Contains(t, got, "Instructions can conflict. Priority, highest first:")
	require.Contains(t, got, "outranks file preferences but not the permission gate")
	require.Contains(t, got, "`priority` number in `<project_context>`")

	gate := strings.Index(got, "permission gate and safety constraints (never overridable)")
	user := strings.Index(got, "the user's explicit instruction for this task")
	require.NotEqual(t, -1, gate, "ladder must name the safety floor")
	require.NotEqual(t, -1, user, "ladder must name the user layer")
	require.Less(t, gate, user, "the permission gate must outrank the user's instruction")
}

func TestBuildMCPCatalog(t *testing.T) {
	none := Build("", nil, false, 0, nil)
	require.NotContains(t, none, "# MCP")
	require.NotContains(t, none, "External docs/URLs")
	got := Build("", nil, false, 0, []string{"browsermcp", "github"})
	require.Contains(t, got, "# MCP")
	require.Contains(t, got, "- browsermcp")
	require.Contains(t, got, "- github")
	require.Contains(t, got, "mcp_list")
	require.Contains(t, got, "mcp_inspect")
	require.Contains(t, got, "mcp_call")
	require.Contains(t, got, "docs/URLs")
	require.NotContains(t, got, `"properties"`)
	require.NotContains(t, got, "inputSchema")
}

// TestBuildUsesGivenCwd pins PP-03: the prompt describes the session's cwd and
// loads that workspace's instructions, not the process cwd's.
func TestBuildUsesGivenCwd(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".git"))
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "workspace-local rule")
	nested := filepath.Join(root, "pkg", "deep")
	mustMkdir(t, nested)

	got := Build(nested, nil, false, 0, nil)
	require.Contains(t, got, "- Current directory: "+nested)
	require.Contains(t, got, "- Workspace: "+root)
	require.Contains(t, got, "workspace-local rule")
}

func TestBuildIgnoresProcessCwdWhenGivenACwd(t *testing.T) {
	processCwd, err := os.Getwd()
	require.NoError(t, err)

	got := Build(t.TempDir(), nil, false, 0, nil)
	require.NotContains(t, got, "- Current directory: "+processCwd)
	require.NotContains(t, got, "- Workspace: "+processCwd)
}

func TestBuildEmptyCwdFallsBackToProcessCwd(t *testing.T) {
	processCwd, err := os.Getwd()
	require.NoError(t, err)

	got := Build("", nil, false, 0, nil)
	require.Contains(t, got, "- Current directory: "+processCwd)
}

// TestWorkspaceDir pins the git-root lookup used for the Workspace line.
func TestWorkspaceDir(t *testing.T) {
	require.Empty(t, workspaceDir(""), "empty dir has no workspace")

	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".git"))
	nested := filepath.Join(root, "a", "b")
	mustMkdir(t, nested)

	require.Equal(t, root, workspaceDir(nested))
	require.Equal(t, root, workspaceDir(root))
}

// TestBuildSkillCatalogMergesRoots pins the priority order: every root
// contributes skills, and the first root wins a duplicate name.
func TestBuildSkillCatalogMergesRoots(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	writeSkill(t, first, "alpha", "from the first root")
	writeSkill(t, second, "alpha", "shadowed by the first root")
	writeSkill(t, second, "beta", "from the second root")

	got := Build("", []string{first, second}, false, 0, nil)

	require.Contains(t, got, "from the first root")
	require.Contains(t, got, "from the second root")
	require.NotContains(t, got, "shadowed by the first root")
}

func writeSkill(t *testing.T, root, name, description string) {
	t.Helper()
	file := filepath.Join(root, name, "SKILL.md")
	mustMkdir(t, filepath.Dir(file))
	mustWrite(t, file, "---\nname: "+name+"\ndescription: "+description+"\n---\nbody\n")
}
