package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/mcp"
)

func TestMCPListMixedTransports(t *testing.T) {
	servers := map[string]mcp.ServerConfig{
		"z-local": {Command: []string{"node", "server.js"}, Args: []string{"--stdio"}},
		"c-sse":   {Transport: "sse", URL: "https://example.test/sse"},
		"b-stream": {
			Transport: "streamable-http",
			URL:       "https://example.test/stream",
		},
		"a-http": {
			Transport: "http",
			URL:       "https://example.test/mcp",
			Headers:   map[string]string{"Authorization": "Bearer test-token"},
		},
		"m-typo": {Transport: "stido"},
	}
	var out strings.Builder
	writeMCPList(&out, servers)

	want := "NAME      TRANSPORT  ADDRESS\n" +
		"a-http    http       https://example.test/mcp\n" +
		"b-stream  http       https://example.test/stream\n" +
		"c-sse     http       https://example.test/sse\n" +
		"m-typo    stido      \n" +
		"z-local   stdio      node server.js --stdio\n"
	require.Equal(t, want, out.String())
}

func TestMCPListEmpty(t *testing.T) {
	var out strings.Builder
	writeMCPList(&out, nil)

	want := "(no servers — try: phi mcp add fetch -- npx -y @modelcontextprotocol/server-fetch)\n"
	require.Equal(t, want, out.String())
}

func TestMCPDoctorWithoutServersMatchesList(t *testing.T) {
	var doctor, list strings.Builder

	require.NoError(t, writeMCPDoctor(&doctor, false, nil))
	writeMCPList(&list, nil)
	require.Equal(t, list.String(), doctor.String())
}

func TestMCPDoctorAllServersAnswered(t *testing.T) {
	var out strings.Builder
	err := writeMCPDoctor(&out, true, []mcp.DoctorResult{
		{Name: "browsermcp", OK: true, Detail: "2 tools"},
		{Name: "fetch", OK: true, Detail: "3 tools"},
	})

	require.NoError(t, err)
	require.Equal(t, "ok\tbrowsermcp\t2 tools\nok\tfetch\t3 tools\n", out.String())
}

func TestMCPDoctorUnreachableServerFails(t *testing.T) {
	var out strings.Builder
	err := writeMCPDoctor(&out, true, []mcp.DoctorResult{
		{Name: "fetch", OK: true, Detail: "3 tools"},
		{Name: "browsermcp", OK: false, Detail: "dial tcp 127.0.0.1:1: connect: refused"},
	})

	var ee *exitError
	require.ErrorAs(t, err, &ee)
	require.Equal(t, ExitError, ee.code)
	require.Equal(t, "ok\tfetch\t3 tools\nFAIL\tbrowsermcp\tdial tcp 127.0.0.1:1: connect: refused\n", out.String())
}

// PHI_MCP=off keeps printing its own line and stays a success.
func TestMCPDoctorDisabledIsNotAFailure(t *testing.T) {
	t.Setenv("PHI_MCP", "off")

	require.NoError(t, mcpDoctor())
}
