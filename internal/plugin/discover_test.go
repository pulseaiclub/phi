package plugin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverOrdersRootsAndDirectoriesAndAppliesDefaults(t *testing.T) {
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	writePlugin(t, firstRoot, "zeta", "version: 1.2.3\n", "init.lua")
	writePlugin(t, firstRoot, "alpha", "name: renamed\nentry: start.lua\n", "start.lua")
	writePlugin(t, secondRoot, "beta", "name: second\n", "init.lua")
	writePlugin(t, firstRoot, ".hidden", "", "hidden")
	writePlugin(t, firstRoot, "no-manifest", "", "")
	require.NoError(t, os.Remove(filepath.Join(firstRoot, "no-manifest", "plugin.yaml")))

	found, errs := discover("0.30.1", firstRoot, filepath.Join(t.TempDir(), "missing"), secondRoot)

	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), filepath.Join(firstRoot, "no-manifest", "plugin.yaml"))
	require.Len(t, found, 3)
	assert.Equal(t, descriptor{name: "renamed", entry: filepath.Join(firstRoot, "alpha", "start.lua")}, found[0])
	assert.Equal(
		t,
		descriptor{name: "zeta", version: "1.2.3", entry: filepath.Join(firstRoot, "zeta", "init.lua")},
		found[1],
	)
	assert.Equal(t, descriptor{name: "second", entry: filepath.Join(secondRoot, "beta", "init.lua")}, found[2])
}

func TestDiscoverMinVersionAndStrictFieldsContinue(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "too-new", "min_phi_version: 0.31.0\n", "too-new")
	writePlugin(t, root, "valid", "min_phi_version: 0.30.1\n", "init.lua")
	writePlugin(t, root, "unknown", "unexpected: true\n", "unknown")
	writePlugin(t, root, "after", "", "init.lua")

	found, errs := discover("v0.30.1", root)

	require.Len(t, found, 2)
	assert.Equal(t, "after", found[0].name)
	assert.Equal(t, "valid", found[1].name)
	require.Len(t, errs, 2)
	assert.Contains(t, errs[0].Error(), filepath.Join(root, "too-new", "plugin.yaml"))
	assert.Contains(t, errs[0].Error(), "requires phi")
	assert.Contains(t, errs[1].Error(), filepath.Join(root, "unknown", "plugin.yaml"))
	assert.Contains(t, errs[1].Error(), "field unexpected not found")
}

func TestDiscoverDuplicateNameFirstValidWins(t *testing.T) {
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	writePlugin(t, firstRoot, "first", "name: same\n", "init.lua")
	writePlugin(t, firstRoot, "invalid", "name: same\nentry: missing.lua\n", "")
	writePlugin(t, secondRoot, "second", "name: same\n", "init.lua")

	found, errs := discover("0.30.1", firstRoot, secondRoot)

	require.Len(t, found, 1)
	assert.Equal(t, filepath.Join(firstRoot, "first", "init.lua"), found[0].entry)
	require.Len(t, errs, 2)
	assert.Contains(t, errs[0].Error(), filepath.Join(firstRoot, "invalid", "plugin.yaml"))
	assert.Contains(t, errs[1].Error(), filepath.Join(secondRoot, "second", "plugin.yaml"))
	assert.Contains(t, errs[1].Error(), "duplicate plugin name")
}

func TestDiscoverRejectsUnsafeEntries(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside.lua")
	require.NoError(t, os.WriteFile(outside, []byte("outside"), 0o644))

	writePlugin(t, root, "absolute", "entry: "+filepath.Join(root, "outside.lua")+"\n", "")
	writePlugin(t, root, "escape", "entry: ../outside.lua\n", "")
	writePlugin(t, root, "wrong-extension", "entry: init.txt\n", "")
	writePlugin(t, root, "directory", "entry: sub.lua\n", "")
	require.NoError(t, os.Mkdir(filepath.Join(root, "directory", "sub.lua"), 0o755))
	writePlugin(t, root, "symlink", "entry: linked.lua\n", "")
	linked := filepath.Join(root, "symlink", "linked.lua")
	if err := os.Symlink(outside, linked); err != nil {
		t.Skip("symlink not permitted:", err)
	}
	writePlugin(t, root, "valid", "{}\n", "init.lua")

	found, errs := discover("0.30.1", root)

	require.Len(t, found, 1)
	assert.Equal(t, "valid", found[0].name)
	require.Len(t, errs, 5)
	for _, err := range errs {
		assert.Contains(t, err.Error(), "plugin.yaml")
	}
}

func TestDiscoverRejectsSymlinkedEntryDirectory(t *testing.T) {
	root := t.TempDir()
	outsideRoot := t.TempDir()
	outside := filepath.Join(outsideRoot, "outside")
	require.NoError(t, os.Mkdir(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "init.lua"), []byte("outside"), 0o644))
	writePlugin(t, root, "linked-dir", "entry: scripts/init.lua\n", "")
	if err := os.Symlink(outside, filepath.Join(root, "linked-dir", "scripts")); err != nil {
		t.Skip("symlink not permitted:", err)
	}

	found, errs := discover("0.30.1", root)

	assert.Empty(t, found)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), filepath.Join(root, "linked-dir", "plugin.yaml"))
	assert.Contains(t, errs[0].Error(), "uses symlink")
}

func writePlugin(t *testing.T, root, name, manifest, entry string) {
	t.Helper()
	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(manifest), 0o644))
	if entry != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, entry), nil, 0o644))
	}
}
