package githubrelease

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupChecksum(t *testing.T) {
	t.Parallel()
	sums := filepath.Join(t.TempDir(), "checksums.txt")
	content := strings.Join([]string{
		"",
		"   ",
		"not-a-checksum-line",
		"aaaa1111  phi_0.12.0_linux_amd64.tar.gz",
		"bbbb2222  phi_0.12.0_windows_amd64.zip",
	}, "\n")
	require.NoError(t, os.WriteFile(sums, []byte(content), 0o644))

	got, err := LookupChecksum(sums, "phi_0.12.0_linux_amd64.tar.gz")
	require.NoError(t, err)
	assert.Equal(t, "aaaa1111", got)

	got, err = LookupChecksum(sums, "phi_0.12.0_windows_amd64.zip")
	require.NoError(t, err)
	assert.Equal(t, "bbbb2222", got)

	_, err = LookupChecksum(sums, "missing_asset.tar.gz")
	require.ErrorContains(t, err, "not listed")

	_, err = LookupChecksum(filepath.Join(t.TempDir(), "absent.txt"), "whatever")
	require.ErrorContains(t, err, "read checksums")
}

func TestSHA256File(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "payload")
	require.NoError(t, os.WriteFile(path, []byte("abc"), 0o644))

	got, err := SHA256File(path)
	require.NoError(t, err)
	assert.Equal(t, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", got)

	_, err = SHA256File(filepath.Join(dir, "absent"))
	require.Error(t, err)
}
