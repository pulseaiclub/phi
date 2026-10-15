package urls

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNamespacedURL(t *testing.T) {
	input := "MCP://plugin:name/a%20b/../c?x=1&x=2#part"
	u, err := ParseInternalURL(input)
	require.NoError(t, err)
	assert.Equal(t, "mcp:", u.Protocol)
	assert.Equal(t, "plugin:name", u.Host)
	assert.Equal(t, "plugin:name", u.RawHost)
	assert.Equal(t, "/a%20b/../c", u.RawPathname)
	assert.Equal(t, u.RawPathname, u.Pathname)
	assert.Equal(t, "?x=1&x=2", u.Search)
	assert.Equal(t, "#part", u.Hash)
	assert.Equal(t, input, u.Href)
	assert.Equal(t, input, u.RawHref)
}

func TestInvalidInternalURL(t *testing.T) {
	testcases := []string{
		"",
		"relative/path",
		"/absolute",
		"mcp:name",
		"mcp:/name",
		"1mcp://name",
		"mcp://bad name",
		"mcp://name/%zz",
		"mcp://name/\n",
		`mcp://name\asset`,
	}

	for _, input := range testcases {
		t.Run(input, func(t *testing.T) { _, err := ParseInternalURL(input); require.Error(t, err) })
	}
}
