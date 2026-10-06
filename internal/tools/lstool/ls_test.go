package lstool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLs_RelativePath(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "pkg")
	require.NoError(t, os.Mkdir(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "main.go"), []byte("package pkg"), 0o644))

	t.Chdir(root)

	out, err := runLs(t.Context(), lsInput{Path: "pkg"})
	require.NoError(t, err)
	require.Contains(t, out.Content, "main.go")
	require.True(
		t,
		strings.HasPrefix(out.Content, "pkg/"),
		"expected cwd-relative tree root pkg/, got: %s",
		out.Content,
	)
	require.Equal(t, "pkg", out.Detail)
}

func TestLs_Errors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a.txt")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))

	_, err := runLs(t.Context(), lsInput{Path: file})
	require.Error(t, err, "expected error for file path")
	require.Contains(t, strings.ToLower(err.Error()), "not a directory")
}

func TestLs_MaxDepthStopsExpansion(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "lvl1", "lvl2", "lvl3"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "lvl1", "lvl2", "lvl3", "deep.txt"), []byte("x"), 0o644))

	out, err := runLs(t.Context(), lsInput{
		Path:     root,
		MaxDepth: 3,
		Limit:    100,
	})
	require.NoError(t, err)
	result := out.Content

	require.Contains(t, result, "lvl1"+string(os.PathSeparator))
	require.Contains(t, result, "lvl2"+string(os.PathSeparator))
	require.Contains(t, result, "lvl3"+string(os.PathSeparator))
	require.NotContains(t, result, "deep.txt", "expected output NOT to contain deep.txt at maxDepth=3")
}

func TestLs_LimitTriggersTruncationMessage(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.txt"), []byte("b"), 0o644))

	out, err := runLs(t.Context(), lsInput{
		Path:  root,
		Limit: 1,
	})
	require.NoError(t, err)
	result := out.Content

	require.Contains(t, result, "Tree truncated after 1 files")
	require.Contains(t, result, "limit=<n>")
}

func TestLs_DefaultOptionsApplied(t *testing.T) {
	limit, depth := normalizeOptions(0, 0)
	require.Equal(t, defaultMaxFiles, limit)
	require.Equal(t, defaultMaxDepth, depth)
}

func TestLs_PlainStringPath(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "x.txt"), []byte("x"), 0o644))

	// Pass path as a plain JSON string, not an object. Only the wire path
	// (LsTool().Run) exercises lsInput.UnmarshalJSON.
	raw, err := json.Marshal(root)
	require.NoError(t, err)
	out, err := LsTool().Run(t.Context(), raw)
	require.NoError(t, err)
	require.Contains(t, out.Content, "x.txt")
}

func TestLs_ExactLimitIsNotTruncated(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "only.txt"), []byte("x"), 0o644))

	out, err := runLs(t.Context(), lsInput{Path: root, Limit: 1})
	require.NoError(t, err)
	require.Contains(t, out.Content, "only.txt")
	require.NotContains(t, out.Content, "Tree truncated",
		"a directory holding exactly limit files is not truncated")
}

func TestLs_OverLimitStillTruncates(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.txt"), []byte("b"), 0o644))

	out, err := runLs(t.Context(), lsInput{Path: root, Limit: 1})
	require.NoError(t, err)
	require.Contains(t, out.Content, "Tree truncated after 1 files")
}
