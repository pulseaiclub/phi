package findtool

import (
	"os"
	"path/filepath"
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
