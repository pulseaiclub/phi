package archive

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractUnknownFormat(t *testing.T) {
	t.Parallel()
	err := Extract(t.Context(), "whatever.rar", "rar", t.TempDir())
	require.ErrorContains(t, err, "unknown archive format: rar")
}

func TestExtractZipRejectsTraversal(t *testing.T) {
	t.Parallel()
	// The backslash variant matters on Windows; the check must fire everywhere.
	for _, name := range []string{"../outside", `..\outside`, "sub/../../outside"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			archivePath := filepath.Join(dir, "malicious.zip")
			file, err := os.Create(archivePath)
			require.NoError(t, err)
			writer := zip.NewWriter(file)
			entry, err := writer.Create(name)
			require.NoError(t, err)
			_, err = entry.Write([]byte("payload"))
			require.NoError(t, err)
			require.NoError(t, writer.Close())
			require.NoError(t, file.Close())

			err = Extract(t.Context(), archivePath, "zip", filepath.Join(dir, "out"))
			require.ErrorContains(t, err, "invalid zip path")
			require.NoFileExists(t, filepath.Join(dir, "outside"))
		})
	}
}
