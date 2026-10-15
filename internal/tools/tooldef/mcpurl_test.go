package tooldef

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/urls"
)

func TestParseMCPTarget(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		target  *MCPTarget
		wantErr bool
	}{
		{name: "plain file path", path: "src/main.go", target: nil},
		{name: "http url", path: "https://example.com", target: nil},
		{name: "root", path: "mcp://", target: &MCPTarget{Kind: urls.MCPRoot}},
		{name: "padded root", path: "  mcp://  ", target: &MCPTarget{Kind: urls.MCPRoot}},
		{
			name:   "server",
			path:   "mcp://github/",
			target: &MCPTarget{Kind: urls.MCPServer, Server: "github"},
		},
		{
			name:   "tools listing",
			path:   "mcp://github/tools/",
			target: &MCPTarget{Kind: urls.MCPTools, Server: "github"},
		},
		{
			name:   "tool",
			path:   "mcp://github/tools/search",
			target: &MCPTarget{Kind: urls.MCPTool, Server: "github", Tool: "search"},
		},
		{
			name:   "escaped tool name",
			path:   "mcp://s/tools/a%2Fb",
			target: &MCPTarget{Kind: urls.MCPTool, Server: "s", Tool: "a/b"},
		},
		{name: "truncated hierarchy", path: "mcp://github/tools", wantErr: true},
		{name: "unknown path segment", path: "mcp://github/resources/", wantErr: true},
		{name: "tool with query", path: "mcp://s/tools/t?x=1", wantErr: true},
		{name: "empty server", path: "mcp:///tools/", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMCPTarget(tt.path)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.target, got)
		})
	}
}

func TestParseMCPTargetRejectsFragments(t *testing.T) {
	_, err := ParseMCPTarget("mcp://s/tools/t#frag")
	require.Error(t, err)
}

func TestMCPOperationsWithoutPool(t *testing.T) {
	ctx := t.Context()
	root := &MCPTarget{Kind: urls.MCPRoot}
	tool := &MCPTarget{Kind: urls.MCPTool, Server: "s", Tool: "t"}

	for _, tc := range []struct {
		name string
		fn   func() (string, error)
	}{
		{"list", func() (string, error) { return MCPList(ctx, nil, root) }},
		{"read", func() (string, error) { return MCPRead(ctx, nil, tool) }},
		{"write", func() (string, error) { return MCPWrite(ctx, nil, tool, `{}`) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.fn()
			require.Error(t, err)
			require.ErrorContains(t, err, "no MCP servers configured")
		})
	}
}
