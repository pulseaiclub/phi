package filesearch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveFD(t *testing.T) {
	ResetResolveFDForTest()
	bin, err := ResolveFD()
	if err != nil {
		t.Skip("fd not installed:", err)
	}
	require.NotEmpty(t, bin)
	_, err = os.Stat(bin)
	require.NoError(t, err)
}

func TestSearch(t *testing.T) {
	ResetResolveFDForTest()
	if _, err := ResolveFD(); err != nil {
		t.Skip("fd not installed:", err)
	}

	dir := t.TempDir()
	mustWrite := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	mustWrite("go.mod", "module x\n")
	mustWrite("internal/session/manager.go", "package session\n")
	mustWrite("internal/session/manager_test.go", "package session\n")
	mustWrite("README.md", "# x\n")

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	all, truncated, err := Search(ctx, dir, "", 20)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(all), 4)
	assert.False(t, truncated, "4 files under a limit of 20 must not report truncation: %v", all)

	hits, _, err := Search(ctx, dir, "manager", 10)
	require.NoError(t, err)
	require.NotEmpty(t, hits)
	for _, h := range hits {
		assert.Contains(t, h, "manager")
		assert.NotContains(t, h, "\\")
	}

	// 4 files, limit 2: the list is cut and the caller must be told.
	limited, truncated, err := Search(ctx, dir, "", 2)
	require.NoError(t, err)
	require.Len(t, limited, 2)
	assert.True(t, truncated, "more files exist beyond the limit, so truncated must be true")

	// Exactly at the limit is not truncation: there is nothing more to show.
	exact, truncated, err := Search(ctx, dir, "manager", 2)
	require.NoError(t, err)
	require.Len(t, exact, 2)
	assert.False(t, truncated, "a full page with no further matches must not report truncation")

	none, truncated, err := Search(ctx, dir, "zzz-no-such-file-xyz", 10)
	require.NoError(t, err)
	assert.Empty(t, none)
	assert.False(t, truncated)
}

// A deadline must surface as ErrTimeout, not as the child's "signal: killed".
// The picker shows this text, so it has to be about the search, not the process.
func TestSearchTimeoutIsTyped(t *testing.T) {
	ResetResolveFDForTest()
	if _, err := ResolveFD(); err != nil {
		t.Skip("fd not installed:", err)
	}

	// Already past the deadline, so fd is killed immediately.
	ctx, cancel := context.WithTimeout(t.Context(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)

	_, _, err := Search(ctx, t.TempDir(), "anything", 10)
	require.ErrorIs(t, err, ErrTimeout)
	assert.NotContains(t, err.Error(), "signal:")
}

// A cancelled search reports the cancellation, not a process failure.
func TestSearchCancelled(t *testing.T) {
	ResetResolveFDForTest()
	if _, err := ResolveFD(); err != nil {
		t.Skip("fd not installed:", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, _, err := Search(ctx, t.TempDir(), "anything", 10)
	require.ErrorIs(t, err, context.Canceled)
}

func TestSearchMissingFD(t *testing.T) {
	ResetResolveFDForTest()
	if _, err := ResolveFD(); err == nil {
		t.Skip("fd is installed")
	}
	_, _, err := Search(t.Context(), t.TempDir(), "", 5)
	require.Error(t, err)
}

// The root is passed to fd as an argument, which makes fd read a missing
// pattern as the pattern itself. An empty query must still list files, and
// results must stay relative to cwd rather than leaking the absolute root.
func TestSearchEmptyQueryListsRelativePaths(t *testing.T) {
	ResetResolveFDForTest()
	if _, err := ResolveFD(); err != nil {
		t.Skip("fd not installed:", err)
	}

	dir := t.TempDir()
	for _, rel := range []string{"a.go", "sub/b.go"} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
	}

	got, _, err := Search(t.Context(), dir, "", 20)
	require.NoError(t, err)
	require.Len(t, got, 2)
	for _, p := range got {
		assert.False(t, filepath.IsAbs(p) || strings.HasPrefix(p, dir), "path must be relative to cwd, got %q", p)
	}
}

// A query must match against the path, not just the file name, and the result
// must still be relative.
func TestSearchMatchesDirectorySegment(t *testing.T) {
	ResetResolveFDForTest()
	if _, err := ResolveFD(); err != nil {
		t.Skip("fd not installed:", err)
	}

	dir := t.TempDir()
	p := filepath.Join(dir, "internal", "session", "manager.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))

	got, _, err := Search(t.Context(), dir, "session", 20)
	require.NoError(t, err)
	require.Equal(t, []string{"internal/session/manager.go"}, got)
}

// fd echoes the search root it was given, so the picker strips it back to a
// path relative to cwd. The echo is not always byte-identical (Windows swaps
// separators and drive-letter case), and an unstripped line reaches the @
// picker as an absolute path.
func TestRelToCwd(t *testing.T) {
	tests := []struct {
		name string
		cwd  string
		line string
		want string
	}{
		{
			name: "echoed root",
			cwd:  "/repo",
			line: "/repo/internal/session/manager.go",
			want: "internal/session/manager.go",
		},
		{name: "root itself", cwd: "/repo", line: "/repo", want: ""},
		{
			name: "already relative",
			cwd:  "/repo",
			line: "internal/session/manager.go",
			want: "internal/session/manager.go",
		},
		{
			name: "relative with dot prefix",
			cwd:  "/repo",
			line: "./internal/session/manager.go",
			want: "internal/session/manager.go",
		},
		{name: "outside cwd", cwd: "/repo", line: "/elsewhere/a.go", want: "/elsewhere/a.go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, relToCwd(tt.cwd, tt.line))
		})
	}
}
