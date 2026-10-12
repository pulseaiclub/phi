package findtool

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGlobToRegex_Matches(t *testing.T) {
	cases := []struct {
		pattern string
		path    string // slash form; the backslash form is checked too
		want    bool
	}{
		{"*.go", "a.go", true},
		{"*.go", "dir/a.go", false},
		{"*.go", "a.go.txt", false},
		{"**/*.go", "dir/a.go", true},
		{"**/src/**/*.go", "C:/x/src/pkg/a.go", true},
		{"**/src/**/*.go", "/x/src/a.go", true}, // ** matches zero dirs
		{"**/src/**/*.go", "C:/x/srcx/a.go", false},
		{"**/src/pkg/a.go", "C:/t/src/pkg/a.go", true},
		{"**/src/pkg/a.go", "C:/t/src/pkg/ab.go", false},
		{"**/a?c.go", "C:/t/abc.go", true},
		{"**/a?c.go", "C:/t/ab/c.go", false}, // ? never crosses a separator
		{"**/x{a,b}.go", "C:/t/xb.go", true},
		{"**/x{a,b}.go", "C:/t/xc.go", false},
		{"**/{src,test}/*.go", "C:/x/test/a.go", true},
		{"**/{src,{inner,deep}}/*.go", "C:/x/deep/a.go", true},
		{"**/[abc].go", "C:/t/b.go", true},
		{"**/[!abc].go", "C:/t/b.go", false},
		{"**/[!abc].go", "C:/t/d.go", true},
		{"**/[a-z].go", "C:/t/q.go", true},
		{"**/[]a].go", "C:/t/a.go", true},
		{"a/**/b.txt", "a/b.txt", true},
		{"a/**/b.txt", "a/x/y/b.txt", true},
		{"**", "anything/at/all", true},
		{"foo+.go", "foo+.go", true}, // regex metachars are literal
		{"foo+.go", "fooggo", false},
	}
	for _, tc := range cases {
		re, err := globToRegex(tc.pattern)
		require.NoError(t, err, tc.pattern)
		rx, err := regexp.Compile(re)
		require.NoError(t, err, "compile %s => %s", tc.pattern, re)
		assert.Equal(t, tc.want, rx.MatchString(tc.path), "%s vs %s", tc.pattern, tc.path)
		backslashed := strings.ReplaceAll(tc.path, "/", `\`)
		assert.Equal(t, tc.want, rx.MatchString(backslashed), "%s vs %s", tc.pattern, backslashed)
	}
}

func TestGlobToRegex_BackslashIsSeparator(t *testing.T) {
	re, err := globToRegex(`src\pkg\*.go`)
	require.NoError(t, err)
	rx, err := regexp.Compile(re)
	require.NoError(t, err)
	assert.True(t, rx.MatchString(`src\pkg\a.go`))
	assert.True(t, rx.MatchString(`src/pkg/a.go`))
	assert.False(t, rx.MatchString(`C:\t\src\pkg\a.go`)) // anchored, needs the **/ prefix runFD adds

	re, err = globToRegex(`**\src\pkg\*.go`)
	require.NoError(t, err)
	rx, err = regexp.Compile(re)
	require.NoError(t, err)
	assert.True(t, rx.MatchString(`C:\t\src\pkg\a.go`))
	assert.True(t, rx.MatchString(`C:/t/src/pkg/a.go`))
}

func TestGlobToRegex_Errors(t *testing.T) {
	for _, pattern := range []string{"[", "[!", "a{b", "{a,b"} {
		_, err := globToRegex(pattern)
		require.Error(t, err, pattern)
	}
}
