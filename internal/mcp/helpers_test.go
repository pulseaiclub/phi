package mcp

import (
	"encoding/json"
	"strings"
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

func TestFormatCallResult(t *testing.T) {
	s := strings.Repeat("a", 32001)
	result := FormatCallResult(s, 0)

	require.Len(t, result, 32000+len("\n… truncated (32001 chars total)"))
	require.True(t, strings.HasPrefix(result, strings.Repeat("a", 32000)))
	require.Contains(t, result, "… truncated (32001 chars total)")

	require.Equal(t, "hello", FormatCallResult("hello", 10))

	require.Equal(
		t,
		"hello\n… truncated (6 chars total)",
		FormatCallResult("helloo", 5),
	)
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
