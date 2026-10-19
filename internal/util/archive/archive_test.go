package archive

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractUnknownFormat(t *testing.T) {
	t.Parallel()
	err := Extract(t.Context(), "whatever.rar", "rar", t.TempDir())
	require.ErrorContains(t, err, "unknown archive format: rar")
}

func TestExtractTarGzWithSpacedPaths(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("tar branch test; Windows uses the zip branch")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src", "inner dir")
	require.NoError(t, os.MkdirAll(src, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "file with spaces.txt"), []byte("payload"), 0o644))

	archivePath := filepath.Join(dir, "archive with spaces.tar.gz")
	out, err := exec.CommandContext(t.Context(), "tar", "-czf", archivePath, "-C", filepath.Join(dir, "src"), "inner dir").
		CombinedOutput()
	require.NoError(t, err, string(out))

	dst := filepath.Join(dir, "out")
	require.NoError(t, os.MkdirAll(dst, 0o755))
	require.NoError(t, Extract(t.Context(), archivePath, "tar.gz", dst))

	got, err := os.ReadFile(filepath.Join(dst, "inner dir", "file with spaces.txt"))
	require.NoError(t, err)
	assert.Equal(t, "payload", string(got))
}

func TestExtractTarGzReportsCommandFailure(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("tar branch test; Windows uses the zip branch")
	}
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "broken.tar.gz")
	require.NoError(t, os.WriteFile(archivePath, []byte("not a tar archive"), 0o644))

	err := Extract(t.Context(), archivePath, "tar.gz", dir)
	require.ErrorContains(t, err, "tar:")
}

func TestExtractHonorsCancellation(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("tar branch test; Windows uses the zip branch")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := Extract(ctx, filepath.Join(t.TempDir(), "never-read.tar.gz"), "tar.gz", t.TempDir())
	require.ErrorContains(t, err, "context canceled")
}

// psLiteral quotes a path as a PowerShell single-quoted literal, where
// backslashes and spaces need no escaping.
func psLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func TestExtractZip(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("powershell"); err != nil {
		t.Skip("zip extraction uses PowerShell Expand-Archive")
	}
	dir := t.TempDir()
	payload := filepath.Join(dir, "file with spaces.txt")
	require.NoError(t, os.WriteFile(payload, []byte("payload"), 0o644))

	archivePath := filepath.Join(dir, "archive with spaces.zip")
	ps := fmt.Sprintf(
		"Compress-Archive -LiteralPath %s -DestinationPath %s",
		psLiteral(payload),
		psLiteral(archivePath),
	)
	out, err := exec.CommandContext(t.Context(), "powershell", "-NoProfile", "-Command", ps).
		CombinedOutput()
	require.NoError(t, err, string(out))

	dst := filepath.Join(dir, "out")
	require.NoError(t, os.MkdirAll(dst, 0o755))
	require.NoError(t, Extract(t.Context(), archivePath, "zip", dst))

	got, err := os.ReadFile(filepath.Join(dst, "file with spaces.txt"))
	require.NoError(t, err)
	assert.Equal(t, "payload", string(got))
}
