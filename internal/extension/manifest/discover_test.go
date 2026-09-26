package manifest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverManifest(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "hello")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "phi.yaml"), []byte("name: hello\nexec: ./hello\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "hello"), []byte("#!/bin/true\n"), 0o755))

	found, warns, err := Discover(root, "")
	require.NoError(t, err)
	assert.Empty(t, warns)
	require.Len(t, found, 1)
	assert.Equal(t, "hello", found[0].ID)
}

func TestDiscoverFollowsSymlinkDir(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real-hello")
	require.NoError(t, os.MkdirAll(real, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(real, "phi.yaml"), []byte("name: hello\nexec: ./hello\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(real, "hello"), []byte("#!/bin/true\n"), 0o755))

	extRoot := filepath.Join(root, "extensions")
	require.NoError(t, os.MkdirAll(extRoot, 0o755))
	require.NoError(t, os.Symlink(real, filepath.Join(extRoot, "hello")))

	found, warns, err := Discover(extRoot, "")
	require.NoError(t, err)
	assert.Empty(t, warns)
	require.Len(t, found, 1)
	assert.Equal(t, "hello", found[0].ID)
}

func TestDiscoverIgnoresSubdirWithoutManifest(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "greet")
	require.NoError(t, os.Mkdir(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "greet.go"), []byte("package main\n"), 0o644))

	found, warns, err := Discover(dir, "")
	require.NoError(t, err)
	assert.Empty(t, found)
	assert.Empty(t, warns)
}

func TestExtensionsDisabled(t *testing.T) {
	t.Setenv(EnvExtensions, "off")
	root := t.TempDir()
	dir := filepath.Join(root, "hello")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "phi.yaml"), []byte("name: hello\nexec: ./x\n"), 0o644))
	found, _, err := Discover(root, "")
	require.NoError(t, err)
	assert.Empty(t, found)
}
