package readtool_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/tools/readtool"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

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

func TestReadShowsMCPToolParams(t *testing.T) {
	pool := newFakeMCPPool(t)
	ctx := t.Context()
	read := readtool.ReadTool(pool)

	res, err := read.Run(ctx, json.RawMessage(`{"path":"mcp://fake/tools/echo"}`))
	require.NoError(t, err)
	assert.Contains(t, res.Content, "@mcp fake/tools/echo")
	assert.Contains(t, res.Content, "echo")
	assert.Contains(t, res.Content, "text")
	assert.Equal(t, "mcp://fake/tools/echo", res.Detail)
}

func TestReadMCPRequiresToolEndpoint(t *testing.T) {
	pool := newFakeMCPPool(t)
	read := readtool.ReadTool(pool)

	_, err := read.Run(t.Context(), json.RawMessage(`{"path":"mcp://fake/tools/"}`))
	require.Error(t, err)
	assert.ErrorContains(t, err, "read expects")
}

func TestReadMCPWithoutPoolFailsClearly(t *testing.T) {
	read := readtool.ReadTool(nil)
	_, err := read.Run(t.Context(), json.RawMessage(`{"path":"mcp://fake/tools/echo"}`))
	require.Error(t, err)
	assert.ErrorContains(t, err, "no MCP servers configured")
}

func TestReadFileStillWorksWithoutPool(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello"), 0o644))
	ctx := tooldef.WithCwd(t.Context(), dir)
	read := readtool.ReadTool(nil)
	res, err := read.Run(ctx, json.RawMessage(`{"path":"note.txt"}`))
	require.NoError(t, err)
	assert.Contains(t, res.Content, "hello")
}
