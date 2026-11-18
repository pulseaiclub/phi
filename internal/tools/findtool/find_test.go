package findtool

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/util/filesearch"
)

func requireFD(t *testing.T) {
	t.Helper()
	filesearch.ResetResolveFDForTest()
	if _, err := filesearch.ResolveFD(); err != nil {
		t.Skip("fd not installed:", err)
	}
}

func TestRenderFindResult(t *testing.T) {
	assert.Equal(t, "No files found", renderFindResult(nil, false, 100))
	assert.Equal(
		t,
		"/a\n/b\n(100 results limit reached. Use limit=200 for more, or refine pattern/path.)",
		renderFindResult([]string{"/a", "/b"}, true, 100),
	)
}

// The row already paints the tool name; the detail must not repeat it.
func TestFindDetail(t *testing.T) {
	assert.Equal(t, `"**/*.go" in src`, findDetail(findInput{Pattern: "**/*.go", Path: "src"}))
	assert.Equal(t, `"**/*.go" in .`, findDetail(findInput{Pattern: " **/*.go "}))
	assert.Equal(t, "src", findDetail(findInput{Path: "src"}))
}

// fd is asked for one result more than the page, so a page that exactly fills
// the limit is complete. Only the extra result proves there is more to show.
func TestParseFDOutputTruncation(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a.txt")
	b := filepath.Join(root, "b.txt")
	c := filepath.Join(root, "c.txt")

	files, truncated := parseFDOutput(a+"\n"+b, root, 2)
	require.False(t, truncated, "exactly the page size is not truncation")
	require.Equal(t, []string{a, b}, files)

	files, truncated = parseFDOutput(strings.Join([]string{a, b, c}, "\n"), root, 2)
	require.True(t, truncated)
	require.Equal(t, []string{a, b}, files, "the extra result only proves more exist")

	files, truncated = parseFDOutput(a+"\r\n\n"+b+"\n", root, 10)
	require.False(t, truncated)
	require.Equal(t, []string{a, b}, files, "CRLF and blank lines are framing, not paths")

	files, truncated = parseFDOutput("sub/rel.txt\n", root, 10)
	require.False(t, truncated)
	require.Equal(t, []string{filepath.Join(root, "sub", "rel.txt")}, files)

	files, truncated = parseFDOutput("", root, 10)
	require.False(t, truncated)
	require.Nil(t, files)
}

func TestRunFD_MatchLimitAndGitignore(t *testing.T) {
	requireFD(t)

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644))
	sub := filepath.Join(root, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "b.txt"), []byte("b"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "c.go"), []byte("c"), 0o644))

	ignored := filepath.Join(root, "node_modules")
	require.NoError(t, os.MkdirAll(ignored, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ignored, "x.txt"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("node_modules/\n"), 0o644))

	files, truncated, err := runFD(t.Context(), "**/*.txt", root, 10)
	require.NoError(t, err)
	require.Len(t, files, 2)
	assert.False(t, truncated)
	for _, f := range files {
		assert.NotContains(t, f, "node_modules")
		assert.True(t, strings.HasSuffix(strings.ToLower(f), ".txt"))
	}

	limited, truncated, err := runFD(t.Context(), "**/*.txt", root, 1)
	require.NoError(t, err)
	require.Len(t, limited, 1)
	assert.True(t, truncated)
}

func TestFind_DefaultPathAndCaseInsensitive(t *testing.T) {
	requireFD(t)

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "ONE.TS"), []byte("x"), 0o644))

	t.Chdir(root)

	out, err := runFind(t.Context(), findInput{Pattern: "*.ts"})
	require.NoError(t, err)
	assert.Equal(t, "ONE.TS", strings.TrimSpace(out.Content))
}

func TestFind_SearchSubdirReturnsCwdRelative(t *testing.T) {
	requireFD(t)

	root := t.TempDir()
	sub := filepath.Join(root, "src")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "a.go"), []byte("x"), 0o644))

	t.Chdir(root)

	out, err := runFind(t.Context(), findInput{Pattern: "*.go", Path: "src"})
	require.NoError(t, err)
	assert.Equal(t, "src/a.go", strings.TrimSpace(out.Content))
}

func TestFind_PathPatternUsesFullPath(t *testing.T) {
	requireFD(t)

	root := t.TempDir()
	nested := filepath.Join(root, "src", "pkg")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(nested, "a.go"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "other.go"), []byte("y"), 0o644))

	t.Chdir(root)

	out, err := runFind(t.Context(), findInput{Pattern: "src/**/*.go"})
	require.NoError(t, err)
	assert.Equal(t, "src/pkg/a.go", strings.TrimSpace(out.Content))
}

func TestFind_PathPatternLiteralFileName(t *testing.T) {
	requireFD(t)

	root := t.TempDir()
	nested := filepath.Join(root, "src", "pkg")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(nested, "a.go"), []byte("x"), 0o644))

	t.Chdir(root)

	out, err := runFind(t.Context(), findInput{Pattern: "src/pkg/a.go"})
	require.NoError(t, err)
	assert.Equal(t, "src/pkg/a.go", strings.TrimSpace(out.Content))

	out, err = runFind(t.Context(), findInput{Pattern: `src\pkg\*.go`})
	require.NoError(t, err)
	assert.Equal(t, "src/pkg/a.go", strings.TrimSpace(out.Content))
}

// TestPatternArgs pins the exact fd argv for both platform paths. Every Windows
// pattern must carry --full-path: its generated regex always contains a path
// separator, and fd refuses to run (exit 1) when a separator appears without
// --full-path — which runFD would otherwise report as "No files found".
func TestPatternArgs(t *testing.T) {
	t.Run("windows", func(t *testing.T) {
		cases := map[string]string{
			"*.go":          `^(?:.*[/\\])?[^/\\]*\.go$`,
			"src/pkg/a.go":  `^(?:.*[/\\])?src[/\\]pkg[/\\]a\.go$`,
			`src\pkg\*.go`:  `^(?:.*[/\\])?src[/\\]pkg[/\\][^/\\]*\.go$`,
			"**/*.go":       `^(?:.*[/\\])?[^/\\]*\.go$`,
			"/abs/a.go":     `^[/\\]abs[/\\]a\.go$`,
			"[a-z][0-9].md": `^(?:.*[/\\])?[a-z][0-9]\.md$`,
		}
		for pattern, want := range cases {
			args, err := appendWindowsPattern(nil, pattern)
			require.NoError(t, err, pattern)
			assert.Equal(t, []string{"--full-path", "--", want}, args, pattern)
		}
	})

	t.Run("posix", func(t *testing.T) {
		cases := map[string][]string{
			"*.go":         {"--glob", "--", "*.go"},
			"src/pkg/a.go": {"--glob", "--full-path", "--", "**/src/pkg/a.go"},
			`src\pkg\*.go`: {"--glob", "--full-path", "--", "**/src/pkg/*.go"},
			"**/*.go":      {"--glob", "--full-path", "--", "**/*.go"},
			"/abs/a.go":    {"--glob", "--full-path", "--", "/abs/a.go"},
		}
		for pattern, want := range cases {
			assert.Equal(t, want, appendGlobPattern(nil, pattern), pattern)
		}
	})
}

// TestFDRunError pins the rule that kept a broken pattern from being reported as
// an absent file: any stderr text from fd is a failure, never "no matches".
func TestFDRunError(t *testing.T) {
	// fd rejects the pattern: the error must name the pattern, not report an
	// empty result set.
	err := fdRunError("*.go", "", "[fd error]: The search pattern 'x' contains a path-separation character")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid glob pattern")
	assert.Contains(t, err.Error(), "*.go")

	err = fdRunError("*.go", "", "error parsing glob: unclosed character class")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid glob pattern")

	// Unreadable root: not the pattern's fault, but still not "no matches".
	err = fdRunError("*.go", "", "[fd error]: Search path 'nope' is not a directory.")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fd: ")

	assert.NoError(t, fdRunError("*.go", "", ""))         // exit with empty stderr: no matches
	assert.NoError(t, fdRunError("*.go", "a.go", "warn")) // partial output: keep it
}

func TestFind_Errors(t *testing.T) {
	requireFD(t)

	_, err := runFind(t.Context(), findInput{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pattern is required")

	_, err = runFind(t.Context(), findInput{Pattern: "["})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid glob pattern")
	missing := filepath.Join(t.TempDir(), "missing")
	_, err = runFind(t.Context(), findInput{Pattern: "*.go", Path: missing})
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "path not found")
}

// fd receives limit+1, so a limit big enough to overflow turns the argument
// negative and fd rejects the run. This pins the fd argument itself:
// parseFDOutput never sees an fd argument, so parsing tests cannot catch it.
func TestFDArgsLimitCannotOverflow(t *testing.T) {
	root := t.TempDir()

	for _, limit := range []int{math.MaxInt, math.MaxInt - 1, math.MaxInt/2 + 1, maxFindLimit + 1} {
		args, err := fdArgs("*.go", root, limit)
		require.NoError(t, err, "limit %d", limit)

		raw, ok := argValue(args, "--max-results")
		require.True(t, ok, "--max-results missing for limit %d", limit)

		n, err := strconv.Atoi(raw)
		require.NoError(t, err)
		assert.Positive(t, n, "limit %d reached fd as %q", limit, raw)
		assert.Equal(t, maxFindLimit+1, n, "limit %d must be capped at %d", limit, maxFindLimit)
	}

	// At maxFindLimit, the hint must not suggest an impossible limit above the cap.
	hint := renderFindResult([]string{"/a"}, true, normalizeFindLimit(math.MaxInt))
	assert.Contains(t, hint, fmt.Sprintf("(%d results limit reached.", maxFindLimit))
	assert.Contains(t, hint, "Refine pattern/path")
	assert.NotContains(t, hint, "Use limit=")

	// Below maxFindLimit, the suggested retry limit stays within maxFindLimit.
	nearCapHint := renderFindResult([]string{"/a"}, true, maxFindLimit-1000)
	assert.Contains(t, nearCapHint, fmt.Sprintf("limit=%d", maxFindLimit))
}

func argValue(args []string, name string) (string, bool) {
	for i, arg := range args {
		if arg == name && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}
