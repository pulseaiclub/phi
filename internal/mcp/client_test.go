package mcp_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/mcp"
)

func TestConfigLoadSave(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := mcp.ServerConfig{
		Transport: "stdio",
		Command:   []string{"npx"},
		Args:      []string{"-y", "pkg"},
	}
	require.NoError(t, mcp.AddServer("demo", cfg))

	servers, err := mcp.Load(filepath.Join(t.TempDir(), ".phi", "mcp.json"))
	require.NoError(t, err)
	require.Contains(t, servers, "demo", "expected demo server")
	require.Equal(t, []string{"npx"}, servers["demo"].Command, "command mismatch")

	ok, err := mcp.RemoveServer("demo")
	require.NoError(t, err)
	require.True(t, ok, "expected remove ok")
}

func TestCompactAndSlim(t *testing.T) {
	tools := []mcp.ToolDefinition{
		{
			Name:        "echo",
			Description: "Echo back",
			InputSchema: json.RawMessage(
				`{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}`,
			),
		},
	}
	slim := mcp.SlimTool(tools[0])
	require.Contains(t, slim, "echo")
	require.Contains(t, slim, "message:s*")
}

func TestSlimToolEnum(t *testing.T) {
	t.Run("string and integer enums ride the summary", func(t *testing.T) {
		slim := mcp.SlimTool(mcp.ToolDefinition{
			Name: "deploy",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"env":{"type":"string","enum":["dev","prod"]},
					"mode":{"type":"integer","enum":[1,2,3]}
				}
			}`),
		})
		require.Contains(t, slim, "env:s{dev|prod}")
		require.Contains(t, slim, "mode:n{1|2|3}")
	})

	t.Run("enum on untyped property keeps ? type", func(t *testing.T) {
		slim := mcp.SlimTool(mcp.ToolDefinition{
			Name:        "x",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"v":{"enum":["a","b"]}}}`),
		})
		require.Contains(t, slim, "v:?{a|b}")
	})

	t.Run("long enum is capped with a +N tail", func(t *testing.T) {
		values := make([]string, 20)
		for i := range values {
			values[i] = fmt.Sprintf("v%02d", i)
		}
		raw, err := json.Marshal(map[string]any{
			"type":       "object",
			"properties": map[string]any{"v": map[string]any{"type": "string", "enum": values}},
		})
		require.NoError(t, err)
		slim := mcp.SlimTool(mcp.ToolDefinition{Name: "x", InputSchema: raw})
		require.Contains(t, slim, "v:s{v00|v01|v02|v03|v04|v05|v06|v07|+12}")
	})

	t.Run("schema without enum is unchanged", func(t *testing.T) {
		slim := mcp.SlimTool(mcp.ToolDefinition{
			Name:        "x",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"v":{"type":"boolean"}}}`),
		})
		require.Contains(t, slim, "v:b")
		require.NotContains(t, slim, "{")
	})
}

func TestDisabled(t *testing.T) {
	t.Setenv("PHI_MCP", "off")
	require.True(t, mcp.Disabled(), "expected disabled")
	pool, err := mcp.LoadPool(filepath.Join(t.TempDir(), ".phi", "mcp.json"))
	require.NoError(t, err)
	require.Nil(t, pool, "expected nil pool")
}
