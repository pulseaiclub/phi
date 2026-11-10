package mcp

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeName(t *testing.T) {
	require.Equal(t, "a_b_c", sanitizeName("a/b c"))
	require.Equal(t, "server", sanitizeName(""))
}

func TestJSONRPCErrorErrorNil(t *testing.T) {
	var err *jsonRPCError
	require.Equal(t, "mcp: nil error", err.Error())
}

func TestJSONRPCErrorErrorValue(t *testing.T) {
	err := &jsonRPCError{
		Code:    -32601,
		Message: "x",
	}
	require.Equal(t, "[-32601] x", err.Error())
}

func TestExtractToolContent(t *testing.T) {
	require.Empty(t, extractToolContent(nil))

	raw := json.RawMessage(`{invalid}`)
	require.Equal(t, string(raw), extractToolContent(raw))

	raw = json.RawMessage(`{
		"content": [
			{
				"type": "image",
				"data": "abc"
			}
		]
	}`)
	require.Equal(t, string(raw), extractToolContent(raw))

	raw = json.RawMessage(`{
		"content": [
			{
				"type": "text",
				"text": "hello"
			}
		],
		"isError": true
	}`)
	require.Equal(t, "error: hello", extractToolContent(raw))
}

func TestLogDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PHI_MCP_LOG_DIR", dir)

	got, err := LogDir()

	require.NoError(t, err)
	require.Equal(t, dir, got)
	require.DirExists(t, dir)
}

func TestCmdLine(t *testing.T) {
	cfg := ServerConfig{
		Command: []string{"node"},
		Args:    []string{"server.js", "--port", "3000"},
	}

	got, err := cfg.CmdLine()

	require.NoError(t, err)
	require.Equal(t, []string{"node", "server.js", "--port", "3000"}, got)

	empty := ServerConfig{}

	got, err = empty.CmdLine()

	require.Nil(t, got)
	require.EqualError(t, err, "empty command")
}
