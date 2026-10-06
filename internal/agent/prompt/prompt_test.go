package prompt

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildAgentsEnabledToggle(t *testing.T) {
	with := Build("", true, 4, nil)
	without := Build("", false, 0, nil)

	require.Contains(t, with, "agent_spawn")
	require.Contains(t, with, "Sub-agents:")
	require.Contains(t, with, "At most 4 sub-agents run concurrently")
	require.NotContains(t, without, "agent_spawn")
	require.NotContains(t, without, "sub-agents run concurrently")
	require.Contains(t, without, "`find` / `grep` / `ls` yourself")
}

func TestBuildEditHashCopyIsUnambiguous(t *testing.T) {
	got := Build("", false, 0, nil)
	require.Contains(t, got, "*** SM:EDIT path")
	require.Contains(t, got, "*** SM:FIND")
	require.NotContains(t, got, "Known path or exact symbol")
	require.NotContains(t, got, "creates a new file only")
	require.NotContains(t, got, "fails if it already exists")
	require.Contains(t, got, "`write` creates or overwrites")
	require.Contains(t, got, "Prefer cwd-relative paths")
}

func TestBuildMCPCatalog(t *testing.T) {
	none := Build("", false, 0, nil)
	require.NotContains(t, none, "# MCP")
	require.NotContains(t, none, "External docs/URLs")
	got := Build("", false, 0, []string{"browsermcp", "github"})
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
