package readtool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunRead_RelativeHeader(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main\n"), 0o644))
	t.Chdir(root)

	out, err := runRead(t.Context(), nil, readInput{Path: "src/main.go"})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(out.Content, "@file src/main.go\n"))
	assert.Equal(t, "src/main.go", out.Detail)
}

func TestRunRead_OffsetPastTheLastLine(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644))
	t.Chdir(root)

	out, err := runRead(t.Context(), nil, readInput{Path: "a.txt", Offset: 100})
	require.NoError(t, err)
	assert.Equal(t, "@file a.txt\n(offset 100 is past the last line; this file has 3 lines)\n", out.Content)
}

func TestRunRead_LastLineIsNotFollowedByAPhantomLine(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644))
	t.Chdir(root)

	// The file ends in a newline, so it holds three lines, not four: offset 4
	// has nothing to read and must say so instead of printing "4|".
	out, err := runRead(t.Context(), nil, readInput{Path: "a.txt", Offset: 4})
	require.NoError(t, err)
	assert.Equal(t, "@file a.txt\n(offset 4 is past the last line; this file has 3 lines)\n", out.Content)
}

func TestRunRead_LastLineIsReadableAtItsOwnOffset(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644))
	t.Chdir(root)

	out, err := runRead(t.Context(), nil, readInput{Path: "a.txt", Offset: 3})
	require.NoError(t, err)
	assert.Equal(t, "@file a.txt\n3|three\n", out.Content)
}

func TestRunRead_OneLineFileCountsOneLine(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("only\n"), 0o644))
	t.Chdir(root)

	out, err := runRead(t.Context(), nil, readInput{Path: "a.txt", Offset: 2})
	require.NoError(t, err)
	assert.Equal(t, "@file a.txt\n(offset 2 is past the last line; this file has 1 line)\n", out.Content)
}
