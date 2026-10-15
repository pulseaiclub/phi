package agent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/mcp"
)

var testModel = llm.ModelConfig{Name: "test", APIKey: "x", BaseURL: "http://127.0.0.1:9"}

// WithMCP adds no tools: ls/read/write resolve mcp:// URLs through the
// executor's pool, so the registry stays identical with and without a pool.
func TestEngineMCPAddsNoTools(t *testing.T) {
	pool := mcp.NewPool(map[string]mcp.ServerConfig{
		"echo": {Command: []string{"true"}},
	})

	sess, err := agent.NewSession(agent.WithCwd(t.TempDir()))
	require.NoError(t, err)
	eng, err := agent.NewEngine(testModel, sess, agent.WithMCP(pool))
	require.NoError(t, err)
	for _, name := range []string{"ls", "read", "write", "bash"} {
		assert.True(t, eng.HasTool(name), "missing tool %s", name)
	}
	for _, name := range []string{"mcp_list", "mcp_inspect", "mcp_call"} {
		assert.False(t, eng.HasTool(name), "meta tool %s should no longer exist", name)
	}

	// Explicit child tool list: unchanged, no MCP meta tools anywhere.
	sess2, err := agent.NewSession(agent.WithCwd(t.TempDir()))
	require.NoError(t, err)
	eng2, err := agent.NewEngine(testModel, sess2, agent.WithTools(agent.ChildTools()))
	require.NoError(t, err)
	assert.False(t, eng2.HasTool("mcp_list"))
}
