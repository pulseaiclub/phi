package permission

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractMCPURLKeptVerbatim(t *testing.T) {
	for _, tool := range []string{"write", "read", "ls"} {
		t.Run(tool, func(t *testing.T) {
			req, err := ExtractAt(tool, json.RawMessage(`{"path":"mcp://fake/tools/echo"}`), t.TempDir())
			require.NoError(t, err)
			require.Len(t, req.Paths, 1)
			assert.Equal(t, "mcp://fake/tools/echo", req.Paths[0], "internal URLs must not be cwd-joined")
		})
	}
}

func TestCheckWriteMCPURLAsks(t *testing.T) {
	g, err := NewGate(DefaultPolicy(), t.TempDir())
	require.NoError(t, err)
	dec, reason := g.Check(t.Context(), Request{
		Action: ActionWrite,
		Tool:   "write",
		Paths:  []string{"mcp://fake/tools/echo"},
	})
	require.Equal(t, Ask, dec, "external MCP calls must ask, not inherit the workspace-only deny")
	assert.Contains(t, reason, "mcp://fake/tools/echo")
}

func TestCheckReadMCPURLAllowedByDefault(t *testing.T) {
	g, err := NewGate(DefaultPolicy(), t.TempDir())
	require.NoError(t, err)
	for _, action := range []Action{ActionRead, ActionLs, ActionGrep, ActionFind} {
		dec, _ := g.Check(t.Context(), Request{
			Action: action,
			Tool:   string(action),
			Paths:  []string{"mcp://fake/tools/echo"},
		})
		require.Equal(t, Allow, dec, "%s of an MCP URL is browse-tier", action)
	}
}

func TestCheckReadMCPURLAsksUnderWorkspaceOnlyReads(t *testing.T) {
	policy := DefaultPolicy()
	policy.WorkspaceOnlyReads = true
	g, err := NewGate(policy, t.TempDir())
	require.NoError(t, err)
	dec, _ := g.Check(t.Context(), Request{
		Action: ActionRead,
		Tool:   "read",
		Paths:  []string{"mcp://fake/tools/echo"},
	})
	require.Equal(t, Ask, dec)
}
