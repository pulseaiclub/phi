package tools_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/tools"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

// Wiring test: the pool handed to DefaultTools must actually reach the tool
// handlers, so mcp:// URLs resolve through the aggregator, not just when the
// tools are built one by one.
func TestDefaultToolsRouteMCPURLs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.Number `json:"id"`
			Method string      `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result any
		if req.Method == "tools/call" {
			result = map[string]any{"content": []map[string]any{
				{"type": "text", "text": "wired"},
			}}
		} else {
			result = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	t.Cleanup(srv.Close)
	pool := mcp.NewPool(map[string]mcp.ServerConfig{
		"fake": {Transport: "http", URL: srv.URL},
	})
	t.Cleanup(func() { _ = pool.Close() })

	registry := tooldef.NewRegistry(tools.DefaultTools(pool))
	write, ok := registry["write"]
	require.True(t, ok)
	res, err := write.Run(t.Context(), json.RawMessage(`{"path":"mcp://fake/tools/echo","content":"{}"}`))
	require.NoError(t, err)
	assert.Contains(t, res.Content, "wired")
	assert.Equal(t, "fake/echo", res.Detail)
}

// Without a pool the same URL fails with an actionable error instead of being
// treated as a file path.
func TestDefaultToolsWithoutPoolRejectMCPURLs(t *testing.T) {
	registry := tooldef.NewRegistry(tools.DefaultTools(nil))
	write, ok := registry["write"]
	require.True(t, ok)
	_, err := write.Run(t.Context(), json.RawMessage(`{"path":"mcp://fake/tools/echo","content":"{}"}`))
	require.Error(t, err)
	assert.ErrorContains(t, err, "no MCP servers configured")
}
