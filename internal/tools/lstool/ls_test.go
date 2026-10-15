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

	out, err := runLs(t.Context(), nil, lsInput{Path: "pkg"})
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

	_, err := runLs(t.Context(), nil, lsInput{Path: file})
	require.Error(t, err, "expected error for file path")
	require.Contains(t, strings.ToLower(err.Error()), "not a directory")
}

func TestLs_MaxDepthStopsExpansion(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "lvl1", "lvl2", "lvl3"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "lvl1", "lvl2", "lvl3", "deep.txt"), []byte("x"), 0o644))

	out, err := runLs(t.Context(), nil, lsInput{
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

	out, err := runLs(t.Context(), nil, lsInput{
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
	// (LsTool(nil).Run) exercises lsInput.UnmarshalJSON.
	raw, err := json.Marshal(root)
	require.NoError(t, err)
	out, err := LsTool(nil).Run(t.Context(), raw)
	require.NoError(t, err)
	require.Contains(t, out.Content, "x.txt")
}

func TestLs_ExactLimitIsNotTruncated(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "only.txt"), []byte("x"), 0o644))

	out, err := runLs(t.Context(), nil, lsInput{Path: root, Limit: 1})
	require.NoError(t, err)
	require.Contains(t, out.Content, "only.txt")
	require.NotContains(t, out.Content, "Tree truncated",
		"a directory holding exactly limit files is not truncated")
}

func TestLs_OverLimitStillTruncates(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.txt"), []byte("b"), 0o644))

	out, err := runLs(t.Context(), nil, lsInput{Path: root, Limit: 1})
	require.NoError(t, err)
	require.Contains(t, out.Content, "Tree truncated after 1 files")
}

func TestLs_UnmarshalJSONPlainString(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "plain path", raw: `"src"`, want: "src"},
		{name: "surrounding whitespace trimmed", raw: `"  src  "`, want: "src"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var in lsInput
			require.NoError(t, json.Unmarshal([]byte(tt.raw), &in))
			require.Equal(t, tt.want, in.Path)
		})
	}
}

func TestLs_UnmarshalJSONZeroValue(t *testing.T) {
	for _, raw := range []string{`{}`, `null`} {
		t.Run(raw, func(t *testing.T) {
			var in lsInput
			require.NoError(t, json.Unmarshal([]byte(raw), &in))
			require.Equal(t, lsInput{}, in)
		})
	}
}

func TestLs_UnmarshalJSONObjectPathKeepsSpaces(t *testing.T) {
	var in lsInput
	require.NoError(t, json.Unmarshal([]byte(`{"path":" a ","max_depth":2}`), &in))
	require.Equal(t, " a ", in.Path, "only the bare-string branch trims the path")
	require.Equal(t, 2, in.MaxDepth)
}

func TestLs_UnmarshalJSONWrapsErrors(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty string", raw: `""`},
		{name: "whitespace only", raw: `"   "`},
		{name: "number", raw: `42`},
		{name: "array", raw: `[]`},
		{name: "object with non-string path", raw: `{"path":42}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var in lsInput
			err := json.Unmarshal([]byte(tt.raw), &in)
			require.ErrorContains(t, err, "failed to parse ls arguments")
		})
	}
}

func TestLs_Detail(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "trims surrounding whitespace", path: "  src  ", want: "src"},
		{name: "empty stays empty", path: "", want: ""},
		{name: "whitespace only stays empty", path: "   ", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, lsDetail(lsInput{Path: tt.path}))
		})
	}
}

func TestLs_RunAcceptsObjectForm(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "y.txt"), []byte("y"), 0o644))

	raw, err := json.Marshal(lsInput{Path: root})
	require.NoError(t, err)

	out, err := LsTool(nil).Run(t.Context(), raw)
	require.NoError(t, err)
	require.Contains(t, out.Content, "y.txt")
}
