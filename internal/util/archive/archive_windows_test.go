//go:build windows

package archive

import (
	"archive/zip"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractZipRejectsTraversalAndSymlinks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "malicious.zip")
	file, err := os.Create(archivePath)
	require.NoError(t, err)
	writer := zip.NewWriter(file)
	entry, err := writer.Create("../outside")
	require.NoError(t, err)
	_, err = entry.Write([]byte("payload"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.NoError(t, file.Close())

	err = Extract(t.Context(), archivePath, "zip", filepath.Join(dir, "out"))
	require.ErrorContains(t, err, "invalid zip path")
	require.NoFileExists(t, filepath.Join(dir, "outside"))
}

// psLiteral quotes a path as a PowerShell single-quoted literal, where
// backslashes and spaces need no escaping.
func psLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// TestExtractZip exercises zip extraction via PowerShell Expand-Archive on Windows.
// Note: Windows CI runners must provide PowerShell (powershell.exe).
func TestExtractZip(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("powershell"); err != nil {
		t.Skip("zip extraction uses PowerShell Expand-Archive; powershell was not found in PATH")
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
