//go:build !windows

package archive

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractTarGzWithSpacedPaths(t *testing.T) {
	t.Parallel()
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

func TestExtractTarGzRejectsTraversal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "traversal.tar.gz")
	file, err := os.Create(archivePath)
	require.NoError(t, err)
	gz := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gz)
	require.NoError(t, tarWriter.WriteHeader(&tar.Header{Name: "../outside", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg}))
	_, err = tarWriter.Write([]byte("x"))
	require.NoError(t, err)
	require.NoError(t, tarWriter.Close())
	require.NoError(t, gz.Close())
	require.NoError(t, file.Close())

	err = Extract(t.Context(), archivePath, "tar.gz", filepath.Join(dir, "out"))
	require.ErrorContains(t, err, "invalid tar path")
	require.NoFileExists(t, filepath.Join(dir, "outside"))
}

func TestExtractTarGzReportsCommandFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "broken.tar.gz")
	require.NoError(t, os.WriteFile(archivePath, []byte("not a tar archive"), 0o644))

	err := Extract(t.Context(), archivePath, "tar.gz", dir)
	require.Error(t, err)
}

func TestExtractHonorsCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := Extract(ctx, filepath.Join(t.TempDir(), "never-read.tar.gz"), "tar.gz", t.TempDir())
	require.ErrorContains(t, err, "context canceled")
}
