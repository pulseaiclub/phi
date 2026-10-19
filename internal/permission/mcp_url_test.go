package permission

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckWriteMCPURLAsks(t *testing.T) {
	g, err := NewGate(DefaultPolicy(), t.TempDir())
	require.NoError(t, err)
	dec, reason := g.Check(t.Context(), Request{
		Kind:  KindWrite,
		Tool:  "write",
		Paths: []string{"mcp://fake/tools/echo"},
	})
	require.Equal(t, Ask, dec, "external MCP calls must ask, not inherit the workspace-only deny")
	assert.Contains(t, reason, "mcp://fake/tools/echo")
}

func TestCheckReadMCPURLAllowedByDefault(t *testing.T) {
	g, err := NewGate(DefaultPolicy(), t.TempDir())
	require.NoError(t, err)
	for _, tool := range []string{"read", "ls", "grep", "find"} {
		dec, _ := g.Check(t.Context(), Request{
			Kind:  KindRead,
			Tool:  tool,
			Paths: []string{"mcp://fake/tools/echo"},
		})
		require.Equal(t, Allow, dec, "%s of an MCP URL is browse-tier", tool)
	}
}

func TestCheckReadMCPURLAsksUnderWorkspaceOnlyReads(t *testing.T) {
	policy := DefaultPolicy()
	policy.WorkspaceOnlyReads = true
	g, err := NewGate(policy, t.TempDir())
	require.NoError(t, err)
	dec, _ := g.Check(t.Context(), Request{
		Kind:  KindRead,
		Tool:  "read",
		Paths: []string{"mcp://fake/tools/echo"},
	})
	require.Equal(t, Ask, dec)
}
