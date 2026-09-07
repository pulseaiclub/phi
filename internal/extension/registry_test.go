package extension

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstallMetaRoundtrip(t *testing.T) {
	dir := t.TempDir()
	want := InstallMeta{
		Spec:        Spec{Owner: "alice", Repo: "greet", Ref: "v1.2.3"},
		Source:      SourceRelease,
		ReleaseTag:  "v1.2.3",
		InstalledAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	require.NoError(t, writeInstallMeta(dir, want))

	got, err := readInstallMeta(dir)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestReadInstallMetaMissing(t *testing.T) {
	_, err := readInstallMeta(t.TempDir())
	require.Error(t, err)
	assert.True(t, os.IsNotExist(err))
}

func TestReadInstallMetaCorrupt(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, installMetaFile), []byte("{not json"), 0o600))

	_, err := readInstallMeta(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse")
}

func TestReadInstallMetaRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, installMetaFile), []byte(`{"spec":{},"source":""}`), 0o600))

	_, err := readInstallMeta(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing owner/repo or source")
}

func TestListInstalled(t *testing.T) {
	dir := t.TempDir()

	managed := filepath.Join(dir, "alpha")
	require.NoError(t, os.MkdirAll(managed, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(managed, "phi.yaml"), []byte("name: alpha\nexec: ./alpha\n"), 0o644))
	require.NoError(t, writeInstallMeta(managed, InstallMeta{
		Spec:       Spec{Owner: "alice", Repo: "alpha"},
		Source:     SourceRelease,
		ReleaseTag: "v1.0.0",
	}))

	manual := filepath.Join(dir, "beta")
	require.NoError(t, os.MkdirAll(manual, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(manual, "phi.yaml"), []byte("name: beta\nexec: ./beta\n"), 0o644))

	// Dirs without a manifest and stray files are not extensions.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "no-manifest"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hi"), 0o644))

	got, err := ListInstalled(dir)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "alpha", got[0].ID)
	assert.True(t, got[0].Managed)
	assert.Equal(t, "v1.0.0", got[0].Meta.ReleaseTag)
	assert.Equal(t, "beta", got[1].ID)
	assert.False(t, got[1].Managed)
}

func TestListInstalledEmptyOrMissingDir(t *testing.T) {
	got, err := ListInstalled(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, got)

	got, err = ListInstalled(filepath.Join(t.TempDir(), "nope"))
	require.NoError(t, err)
	assert.Empty(t, got)
}
