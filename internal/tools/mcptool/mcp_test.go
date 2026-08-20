package mcptool_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/tools/mcptool"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

func TestMCPToolsRegister(t *testing.T) {
	pool := mcp.NewPool(map[string]mcp.ServerConfig{
		"echo": {Command: []string{"true"}},
	})
	tools := mcptool.Tools(pool)
	if len(tools) != 3 {
		t.Fatalf("tools = %d", len(tools))
	}
	byName := map[string]bool{}
	for _, tool := range tools {
		byName[tool.Definition.Name] = true
	}
	for _, name := range []string{"mcp_list", "mcp_inspect", "mcp_call"} {
		if !byName[name] {
			t.Fatalf("missing %s", name)
		}
	}
}

func TestMCPToolsNilPool(t *testing.T) {
	if mcptool.Tools(nil) != nil {
		t.Fatal("expected nil")
	}
}

func TestMCPListRequiresServer(t *testing.T) {
	pool := mcp.NewPool(map[string]mcp.ServerConfig{
		"echo": {Command: []string{"true"}},
	})
	list := findTool(t, mcptool.Tools(pool), "mcp_list")
	req := list.Definition.Params.Required
	if len(req) != 1 || req[0] != "server" {
		t.Fatalf("Required = %v, want [server]", req)
	}
	_, err := list.Run(t.Context(), []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "server is required") {
		t.Fatalf("err = %v, want server is required", err)
	}
}

func TestMCPListSupportsRefresh(t *testing.T) {
	pool := mcp.NewPool(map[string]mcp.ServerConfig{
		"echo": {Command: []string{"true"}},
	})
	list := findTool(t, mcptool.Tools(pool), "mcp_list")
	refresh, ok := list.Definition.Params.Properties["refresh"].(map[string]any)
	if !ok || refresh["type"] != "boolean" {
		t.Fatalf("refresh property = %#v, want boolean", list.Definition.Params.Properties["refresh"])
	}
}

func TestMCPListRefreshBypassesAndUpdatesCache(t *testing.T) {
	var listCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if request.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}

		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
			}
		case "tools/list":
			name := "old"
			if listCalls.Add(1) > 1 {
				name = "new"
			}
			result = map[string]any{"tools": []map[string]string{{"name": name}}}
		default:
			http.Error(w, "unexpected method", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      request.ID,
			"result":  result,
		})
	}))
	defer srv.Close()

	pool := mcp.NewPool(map[string]mcp.ServerConfig{
		"echo": {Transport: "http", URL: srv.URL},
	})
	defer func() {
		if err := pool.Close(); err != nil {
			t.Errorf("close pool: %v", err)
		}
	}()
	list := findTool(t, mcptool.Tools(pool), "mcp_list")
	run := func(input string) string {
		t.Helper()
		result, err := list.Run(t.Context(), []byte(input))
		if err != nil {
			t.Fatal(err)
		}
		return result.Content
	}

	if got := run(`{"server":"echo"}`); got != "old" {
		t.Fatalf("first list = %q, want old", got)
	}
	if got := run(`{"server":"echo"}`); got != "old" || listCalls.Load() != 1 {
		t.Fatalf("cached list/calls = %q/%d, want old/1", got, listCalls.Load())
	}
	if got := run(`{"server":"echo","refresh":true}`); got != "new" || listCalls.Load() != 2 {
		t.Fatalf("refreshed list/calls = %q/%d, want new/2", got, listCalls.Load())
	}
	if got := run(`{"server":"echo"}`); got != "new" || listCalls.Load() != 2 {
		t.Fatalf("post-refresh list/calls = %q/%d, want new/2", got, listCalls.Load())
	}
}

func findTool(t *testing.T, tools []tooldef.Tool, name string) tooldef.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Definition.Name == name {
			return tool
		}
	}
	t.Fatalf("missing %s", name)
	return tooldef.Tool{}
}
