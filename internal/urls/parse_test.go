package urls

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNamespacedURL(t *testing.T) {
	input := "SKILL://plugin:name/a%20b/../c?x=1&x=2#part"
	u, err := ParseInternalURL(input)
	require.NoError(t, err)
	assert.Equal(t, "skill:", u.Protocol)
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
		"skill:name",
		"skill:/name",
		"1skill://name",
		"skill://bad name",
		"skill://name/%zz",
		"skill://name/\n",
		`skill://name\asset`,
	}

	for _, input := range testcases {
		t.Run(input, func(t *testing.T) { _, err := ParseInternalURL(input); require.Error(t, err) })
	}
}

func TestExtractURIScheme(t *testing.T) {
	testcases := []struct {
		input    string
		expected string
	}{
		{"SKILL://plugin:name", "skill"},
		{"urn:example:document", "urn"},
		{"custom:item", "custom"},
		{"https://example.com", "https"},
		{"C:\foo", ""},
		{"C:/foo", ""},
		{"C:foo", ""},
		{"foo.ts:50", ""},
		{"Makefile:12", ""},
		{"README:raw:1-20", ""},
		{"notes:conflicts", ""},
		{"notes:1-20,30+5", ""},
		{"notes:-10", ""},
		{"relative/file", ""},
		{"custom:", ""},
	}

	for _, tc := range testcases {
		t.Run(tc.input, func(t *testing.T) { assert.Equal(t, tc.expected, ExtractURIScheme(tc.input)) })
	}
}
