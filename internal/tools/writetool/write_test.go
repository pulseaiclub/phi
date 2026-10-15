package writetool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunWriteCreatesAndOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "out.txt")
	tool := WriteTool(nil)

	created, err := tool.Run(t.Context(), mustWriteArgs(t, path, "first\n"))
	require.NoError(t, err)
	require.Contains(t, created.Content, "wrote")
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "first\n", string(got))

	overwritten, err := tool.Run(t.Context(), mustWriteArgs(t, path, "second\n"))
	require.NoError(t, err)
	require.Contains(t, overwritten.Content, "wrote")
	got, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "second\n", string(got))
}

func TestRunWriteRelativePathInResult(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	tool := WriteTool(nil)

	created, err := tool.Run(t.Context(), mustWriteArgs(t, "nested/out.txt", "first\n"))
	require.NoError(t, err)
	require.Equal(t, "wrote 6 bytes to nested/out.txt", created.Content)
	require.Equal(t, "nested/out.txt", created.Detail)
}

func TestRunWriteRequiresPath(t *testing.T) {
	tool := WriteTool(nil)
	_, err := tool.Run(t.Context(), []byte(`{"path":"","content":"x"}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "path is required")
}

func mustWriteArgs(t *testing.T, path, content string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]string{"path": path, "content": content})
	require.NoError(t, err)
	return b
}
