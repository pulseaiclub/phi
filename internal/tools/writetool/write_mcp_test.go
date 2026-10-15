package writetool_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
	"github.com/pulseaiclub/phi/internal/tools/writetool"
)

func TestWriteInvokesMCPTool(t *testing.T) {
	var mu sync.Mutex
	var gotArgs map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.Number     `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result any
		switch req.Method {
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			require.NoError(t, json.Unmarshal(req.Params, &p))
			require.Equal(t, "echo", p.Name)
			mu.Lock()
			gotArgs = p.Arguments
			mu.Unlock()
			result = map[string]any{"content": []map[string]any{
				{"type": "text", "text": "echoed: hi"},
			}}
		default:
			result = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	t.Cleanup(srv.Close)
	pool := mcp.NewPool(map[string]mcp.ServerConfig{
		"fake": {Transport: "http", URL: srv.URL},
	})
	ctx := t.Context()
	write := writetool.WriteTool(pool)

	res, err := write.Run(ctx, json.RawMessage(`{"path":"mcp://fake/tools/echo","content":"{\"text\":\"hi\"}"}`))
	require.NoError(t, err)
	assert.Contains(t, res.Content, "echoed: hi")
	assert.Equal(t, "fake/echo", res.Detail)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, map[string]any{"text": "hi"}, gotArgs)
}

func TestWriteMCPRejectsNonObjectContent(t *testing.T) {
	pool := mcp.NewPool(map[string]mcp.ServerConfig{"fake": {Transport: "http", URL: "http://127.0.0.1:9"}})
	write := writetool.WriteTool(pool)

	for name, content := range map[string]string{
		"array":  `[1,2]`,
		"scalar": `42`,
		"null":   `null`,
		"text":   `not json`,
	} {
		t.Run(name, func(t *testing.T) {
			args, _ := json.Marshal(map[string]string{"path": "mcp://fake/tools/echo", "content": content})
			_, err := write.Run(t.Context(), args)
			require.Error(t, err)
			assert.ErrorContains(t, err, "JSON object")
		})
	}
}

func TestWriteMCPRequiresToolEndpoint(t *testing.T) {
	pool := mcp.NewPool(map[string]mcp.ServerConfig{"fake": {Transport: "http", URL: "http://127.0.0.1:9"}})
	write := writetool.WriteTool(pool)

	_, err := write.Run(t.Context(), json.RawMessage(`{"path":"mcp://fake/tools/","content":"{}"}`))
	require.Error(t, err)
	assert.ErrorContains(t, err, "write expects")
}

func TestWriteFileStillWorksWithoutPool(t *testing.T) {
	dir := t.TempDir()
	ctx := tooldef.WithCwd(t.Context(), dir)
	write := writetool.WriteTool(nil)
	res, err := write.Run(ctx, json.RawMessage(`{"path":"out.txt","content":"done"}`))
	require.NoError(t, err)
	assert.Contains(t, res.Content, "out.txt")
	data, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	require.NoError(t, err)
	assert.Equal(t, "done", string(data))
}
