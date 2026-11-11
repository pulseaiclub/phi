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
	}
	var out strings.Builder
	writeMCPList(&out, servers)

	want := "a-http\thttps://example.test/mcp\n" +
		"b-stream\thttps://example.test/stream\n" +
		"c-sse\thttps://example.test/sse\n" +
		"z-local\tnode server.js --stdio\n"
	require.Equal(t, want, out.String())
}
