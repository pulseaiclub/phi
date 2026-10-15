package lstool_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/tools/lstool"
)

// newFakeMCPPool serves one MCP server with a single `echo` tool over
// in-process HTTP, exercising the same JSON-RPC path as a real server.
func newFakeMCPPool(t *testing.T) *mcp.Pool {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.Number `json:"id"`
			Method string      `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result any
		switch req.Method {
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name":        "echo",
				"description": "Echo the text back",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text": map[string]any{"type": "string"},
					},
					"required": []string{"text"},
				},
			}}}
		default:
			result = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	t.Cleanup(srv.Close)
	return mcp.NewPool(map[string]mcp.ServerConfig{
		"fake": {Transport: "http", URL: srv.URL},
	})
}

func TestLsRoutesMCPURLs(t *testing.T) {
	pool := newFakeMCPPool(t)
	ctx := t.Context()
	list := lstool.LsTool(pool)

	t.Run("root lists servers", func(t *testing.T) {
		res, err := list.Run(ctx, json.RawMessage(`{"path":"mcp://"}`))
		require.NoError(t, err)
		assert.Contains(t, res.Content, "mcp://fake/tools/")
	})

	t.Run("server tools listing", func(t *testing.T) {
		res, err := list.Run(ctx, json.RawMessage(`{"path":"mcp://fake/tools/"}`))
		require.NoError(t, err)
		assert.Contains(t, res.Content, "mcp://fake/tools/echo")
		assert.Equal(t, "mcp://fake/tools/", res.Detail)
	})

	t.Run("malformed mcp url is an error", func(t *testing.T) {
		_, err := list.Run(ctx, json.RawMessage(`{"path":"mcp://fake/resources/"}`))
		require.Error(t, err)
	})

	t.Run("mcp url without pool fails clearly", func(t *testing.T) {
		plain := lstool.LsTool(nil)
		_, err := plain.Run(ctx, json.RawMessage(`{"path":"mcp://"}`))
		require.Error(t, err)
		assert.ErrorContains(t, err, "no MCP servers configured")
	})

	t.Run("file paths still work without pool", func(t *testing.T) {
		plain := lstool.LsTool(nil)
		res, err := plain.Run(ctx, json.RawMessage(`{"path":"."}`))
		require.NoError(t, err)
		assert.NotEmpty(t, res.Content)
	})
}
