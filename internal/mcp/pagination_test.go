package mcp_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/mcp"
)

// paginatedServer serves tools/list one page at a time and records every
// params object it was asked for.
func paginatedServer(t *testing.T, pages [][]string) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var seen []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		_ = json.Unmarshal(body, &req)

		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}

		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "paged", "version": "0.1"},
			}
		case "tools/list":
			seen = append(seen, req.Params)
			// The cursor is the index of the page to serve; absent means page 0.
			next := 0
			if c, ok := req.Params["cursor"].(string); ok && c != "" {
				_, err := fmt.Sscanf(c, "page-%d", &next)
				require.NoError(t, err)
			}
			require.Less(t, next, len(pages), "server asked for a page it never announced")
			page := map[string]any{"tools": toolsNamed(pages[next])}
			if next+1 < len(pages) {
				page["nextCursor"] = fmt.Sprintf("page-%d", next+1)
			}
			result = page
		default:
			t.Errorf("unexpected method %q", req.Method)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  result,
		}))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func toolsNamed(names []string) []map[string]any {
	out := make([]map[string]any, 0, len(names))
	for _, n := range names {
		out = append(out, map[string]any{
			"name":        n,
			"description": n + " tool",
			"inputSchema": map[string]any{"type": "object"},
		})
	}
	return out
}

// A server splits tools/list into pages until it omits nextCursor. Listing must
// follow those pages: the caller sees every tool, and a tool from a later page
// is inspectable.
func TestListToolsFollowsPagination(t *testing.T) {
	srv, seen := paginatedServer(t, [][]string{
		{"alpha", "beta"},
		{"gamma"},
		{"delta", "epsilon"},
	})
	pool := mcp.NewPool(map[string]mcp.ServerConfig{
		"paged": {Transport: "http", URL: srv.URL},
	})

	tools, err := pool.ListTools(t.Context(), "paged")
	require.NoError(t, err)
	require.Equal(t, []string{"alpha", "beta", "gamma", "delta", "epsilon"}, toolNames(tools))
	require.Len(t, *seen, 3, "one tools/list request per page")

	def, err := pool.Inspect(t.Context(), "paged", "epsilon")
	require.NoError(t, err)
	require.Equal(t, "epsilon tool", def.Description)
}

// A server that pages but never advances its cursor must not spin forever.
func TestListToolsStopsOnRepeatedCursor(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &req)
		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		if req.Method == "initialize" {
			result = map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "stuck", "version": "0.1"},
			}
		} else {
			calls++
			result = map[string]any{"tools": toolsNamed([]string{"alpha"}), "nextCursor": "same"}
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  result,
		}))
	}))
	t.Cleanup(srv.Close)
	pool := mcp.NewPool(map[string]mcp.ServerConfig{
		"stuck": {Transport: "http", URL: srv.URL},
	})

	tools, err := pool.ListTools(t.Context(), "stuck")
	require.ErrorContains(t, err, "repeated")
	require.Nil(t, tools)
	require.LessOrEqual(t, calls, 32, "pagination must be bounded")
}

func toolNames(tools []mcp.ToolDefinition) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}
