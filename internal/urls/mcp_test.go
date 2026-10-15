package urls

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMCPCall(t *testing.T) {
	for _, args := range []string{`{}`, `{"text":"$(touch /tmp/nope); | & > < ` + "`whoami`" + `", "n":9007199254740993, "nested":[1,true,null]}`} {
		call, err := ParseMCPCall("MCP://Server/tools/Tool\t\n" + args + " \n")
		require.NoError(t, err)
		assert.Equal(t, MCPURL{Kind: MCPTool, Server: "Server", Tool: "Tool"}, call.Target)
		require.NotNil(t, call.Args)
		if len(call.Args) != 0 {
			assert.Equal(t, json.Number("9007199254740993"), call.Args["n"])
			assert.Equal(t, "$(touch /tmp/nope); | & > < `whoami`", call.Args["text"])
			assert.Equal(t, []any{json.Number("1"), true, nil}, call.Args["nested"])
		}
	}
}

func TestInvalidMCPCall(t *testing.T) {
	for _, command := range []string{
		"", "echo hello", "https://s/tools/t {}", "mcp://s/tools/t", "mcp://s/tools/t ",
		"mcp://s/tools/t{}", " mcp://s/tools/t {}", "mcp:// {}", "mcp://s/ {}", "mcp://s/tools/ {}",
		"mcp://s/tools/t?limit=1 {}", "mcp://s/tools/t null", "mcp://s/tools/t []", "mcp://s/tools/t 1",
		`mcp://s/tools/t "text"`, "mcp://s/tools/t {", "mcp://s/tools/t {} {}", "mcp://s/tools/t {} null",
		"mcp://s/tools/t {} ; echo injected", "mcp://s/tools/t {} | cat", "mcp://s/tools/t '{}'",
	} {
		t.Run(command, func(t *testing.T) { _, err := ParseMCPCall(command); require.Error(t, err) })
	}
}

func TestParseMCPURL(t *testing.T) {
	tests := []struct {
		input string
		want  MCPURL
	}{
		{"mcp://", MCPURL{Kind: MCPRoot}},
		{"McP://Server/", MCPURL{Kind: MCPServer, Server: "Server"}},
		{"mcp://user@ns:123/tools/", MCPURL{Kind: MCPTools, Server: "user@ns:123"}},
		{"mcp://S/tools/Tool", MCPURL{Kind: MCPTool, Server: "S", Tool: "Tool"}},
		{"mcp://?cursor=a%2Fb%2B%25&limit=1000", MCPURL{Kind: MCPRoot, Cursor: "a/b+%", Limit: 1000}},
		{"mcp://s/?limit=1", MCPURL{Kind: MCPServer, Server: "s", Limit: 1}},
		{"mcp://s/tools/?cursor=next", MCPURL{Kind: MCPTools, Server: "s", Cursor: "next"}},
		{"mcp://a%2Fb/tools/%252F", MCPURL{Kind: MCPTool, Server: "a/b", Tool: "%2F"}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseMCPURL(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			built, err := got.Build()
			require.NoError(t, err)
			again, err := ParseMCPURL(built.Href)
			require.NoError(t, err)
			assert.Equal(t, got, again)
		})
	}
}

func TestMCPURLRoundTripNames(t *testing.T) {
	for _, name := range []string{"CaseSensitive", "a/b", "工具", "ns:tool", "%2F", "?#&=+@", "white space", `a\b`, ".", ".."} {
		t.Run(name, func(t *testing.T) {
			want := MCPURL{Kind: MCPTool, Server: name, Tool: name}
			built, err := want.Build()
			require.NoError(t, err)
			got, err := ParseMCPURL(built.Href)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
}

func TestInvalidMCPURL(t *testing.T) {
	for _, input := range []string{
		"", "https://s/", "mcp:s", "mcp:///", "mcp://s", "mcp://s//", "mcp://s/tools",
		"mcp://s/Tools/", "mcp://s/resources/", "mcp://s/tools/t/", "mcp://s/tools/a/b",
		"mcp://s/%74ools/t", "mcp://s/#", "mcp://#x", "mcp://s/tools/t?", "mcp://s/tools/t?limit=1",
		"mcp://?other=1", "mcp://?limit=1&limit=2", "mcp://?cursor=a&cursor=b", "mcp://?limit=1&%6cimit=2",
		"mcp://?limit=0", "mcp://?limit=-1", "mcp://?limit=1001", "mcp://?limit=", "mcp://?limit=1.5",
		"mcp://?limit=%2B1", "mcp://?limit=999999999999999999999999", "mcp://?cursor=%zz", "mcp://?cursor=a;b",
		"mcp://%ff/", "mcp://%00/", "mcp://s/tools/%7F", "mcp://s/tools/%C2%85", "mcp://s/tools/%", "mcp://bad name/",
	} {
		t.Run(input, func(t *testing.T) { _, err := ParseMCPURL(input); require.Error(t, err) })
	}
}

func TestInvalidMCPURLBuild(t *testing.T) {
	for _, target := range []MCPURL{
		{},
		{Kind: MCPURLKind(99)},
		{Kind: MCPRoot, Server: "s"},
		{Kind: MCPRoot, Tool: "t"},
		{Kind: MCPServer},
		{Kind: MCPTools, Server: "s", Tool: "t"},
		{Kind: MCPTool, Server: "s"},
		{Kind: MCPTool, Server: "s", Tool: "t", Cursor: "next"},
		{Kind: MCPRoot, Limit: -1},
		{Kind: MCPRoot, Limit: 1001},
		{Kind: MCPServer, Server: "\xff"},
		{Kind: MCPTool, Server: "s", Tool: "\n"},
	} {
		_, err := target.Build()
		require.Error(t, err, "%+v", target)
	}
}
