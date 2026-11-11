package create

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/util/platform"
)

// sampleMain is a minimal, buildable PXB extension entry point. It imports the
// author SDK so that go mod tidy keeps the ext/go requirement and go build links
// a real binary, exercising the same toolchain path the sibling extension tests
// rely on (see internal/extension/load_test.go).
const sampleMain = `package main

import "github.com/pulseaiclub/phi/ext/go/phi"

func main() {
	_ = phi.New("sample", "0.1.0").Run()
}
`

// TestMaterializeWritesModuleMainAndManifest covers the happy path: the module
// file, entry point, manifest, and compiled binary all land in dir with the
// expected contents.
func TestMaterializeWritesModuleMainAndManifest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sample")
	require.NoError(t, Materialize(t.Context(), dir, "sample", "0.1.0", sampleMain))

	gomod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	require.NoError(t, err)
	assert.Contains(t, string(gomod), "module sample")

	main, err := os.ReadFile(filepath.Join(dir, "main.go"))
	require.NoError(t, err)
	assert.Equal(t, sampleMain, string(main))

	bin := platform.ExecutableName("extbin")
	manifest, err := os.ReadFile(filepath.Join(dir, "phi.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(manifest), "name: sample")
	assert.Contains(t, string(manifest), `version: "0.1.0"`)
	assert.Contains(t, string(manifest), "exec: ./"+bin)

	assert.FileExists(t, filepath.Join(dir, bin))
}

// TestMaterializeOverwritesExistingDirectory documents the real behavior: the
// target dir may already exist and hold other files. Materialize creates it when
// missing and overwrites go.mod/main.go rather than failing on a populated
// target, while leaving unrelated files in place.
func TestMaterializeOverwritesExistingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sample")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	keep := filepath.Join(dir, "keep.txt")
	require.NoError(t, os.WriteFile(keep, []byte("untouched"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module stale\n"), 0o644))

	require.NoError(t, Materialize(t.Context(), dir, "fresh", "0.2.0", sampleMain))

	gomod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	require.NoError(t, err)
	assert.Contains(t, string(gomod), "module fresh")
	assert.NotContains(t, string(gomod), "module stale")

	kept, err := os.ReadFile(keep)
	require.NoError(t, err)
	assert.Equal(t, "untouched", string(kept))
}

// TestMaterializeBuildFailurePropagates ensures a main.go that fails to compile
// surfaces as an error from the go build step, and that no manifest is written
// for a broken extension.
func TestMaterializeBuildFailurePropagates(t *testing.T) {
	broken := "package main\n\nfunc main() { _ = undefinedSymbol }\n"
	dir := filepath.Join(t.TempDir(), "broken")

	err := Materialize(t.Context(), dir, "broken", "0.0.1", broken)
	require.Error(t, err)
	assert.ErrorContains(t, err, "go build")
	assert.NoFileExists(t, filepath.Join(dir, "phi.yaml"))
}
