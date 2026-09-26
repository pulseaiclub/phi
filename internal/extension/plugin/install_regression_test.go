package plugin

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/util/githubrelease"
)

func TestPlaceTreeCopyFailurePreservesOld(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture requires Unix")
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "plugin")
	require.NoError(t, os.Mkdir(dest, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dest, "marker"), []byte("old"), 0o644))
	staging := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(staging, "a"), []byte("new"), 0o644))
	require.NoError(t, os.Symlink("missing", filepath.Join(staging, "z")))
	require.ErrorContains(t, placeTree(staging, dest, true), "stage replacement")
	got, err := os.ReadFile(filepath.Join(dest, "marker"))
	require.NoError(t, err)
	assert.Equal(t, "old", string(got))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "plugin", entries[0].Name())
}

func TestPlaceTreeRefusesUnresolvedBackup(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "plugin")
	backup := filepath.Join(dir, ".plugin.old")
	for _, path := range []string{dest, backup} {
		require.NoError(t, os.Mkdir(path, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(path, "marker"), []byte(path), 0o644))
	}
	require.ErrorContains(t, placeTree(t.TempDir(), dest, true), "unresolved backup")
	for _, path := range []string{dest, backup} {
		got, err := os.ReadFile(filepath.Join(path, "marker"))
		require.NoError(t, err)
		assert.Equal(t, path, string(got))
	}
}

func TestUpdateExplicitRefPersists(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tar.gz release path covered on unix CI")
	}
	for _, tc := range []struct{ name, ref, tag, wantRef string }{
		{"unpin current", "latest", "v2.0.0", ""},
		{"downgrade", "v1.0.0", "v1.0.0", "v1.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			managedPlugin(t, dir, InstallMeta{
				Spec:   Spec{Owner: "alice", Repo: "greet", Ref: "v2.0.0"},
				Source: SourceRelease, ReleaseTag: "v2.0.0",
			}, "old")
			rel, assets := releaseWithAsset(t, tc.tag, "greet", map[string]string{
				"phi.yaml": "name: greet\nexec: ./greet\n", "greet": "new",
			})
			var out bytes.Buffer
			opts := UpdateOptions{
				Dir: dir, ID: "greet", Ref: tc.ref, Check: true, Stdout: &out,
				FetchRelease: func(_ context.Context, _, ref string) (githubrelease.Release, error) {
					assert.Equal(t, tc.wantRef, ref)
					return rel, nil
				},
				DownloadFile: serveDownloads(t, assets),
			}
			require.NoError(t, Update(t.Context(), opts))
			assert.Contains(t, out.String(), "update available -> "+tc.tag)
			opts.Check = false
			require.NoError(t, Update(t.Context(), opts))
			meta, err := readInstallMeta(filepath.Join(dir, "greet"))
			require.NoError(t, err)
			assert.Equal(t, tc.wantRef, meta.Spec.Ref)
			assert.Equal(t, tc.tag, meta.ReleaseTag)
		})
	}
}

func TestUpdateRemoveUseActualPath(t *testing.T) {
	for _, id := range []string{"greet", "alice/greet"} {
		t.Run(id, func(t *testing.T) {
			dir := t.TempDir()
			managedPlugin(t, dir, InstallMeta{
				Spec: Spec{Owner: "alice", Repo: "greet"}, Source: SourceGit,
			}, "old")
			actual := filepath.Join(dir, "different-directory")
			require.NoError(t, os.Rename(filepath.Join(dir, "greet"), actual))
			managedPlugin(t, dir, InstallMeta{
				Spec: Spec{Owner: "bob", Repo: "other"}, Source: SourceGit,
			}, "other plugin")
			require.NoError(t, os.WriteFile(filepath.Join(dir, "greet", "phi.yaml"),
				[]byte("name: other\nexec: ./greet\n"), 0o644))
			require.NoError(t, Update(t.Context(), UpdateOptions{
				Dir: dir, ID: id, Git: "git",
				FetchRelease: func(context.Context, string, string) (githubrelease.Release, error) {
					return githubrelease.Release{}, os.ErrNotExist
				},
				RunGit: func(_ context.Context, _ string, args ...string) error {
					dest := args[len(args)-1]
					require.NoError(t, os.MkdirAll(dest, 0o755))
					require.NoError(t, os.WriteFile(filepath.Join(dest, "phi.yaml"),
						[]byte("name: greet\nexec: ./greet\n"), 0o644))
					return os.WriteFile(filepath.Join(dest, "greet"), []byte("new"), 0o755)
				},
			}))
			got, err := os.ReadFile(filepath.Join(actual, "greet"))
			require.NoError(t, err)
			assert.Equal(t, "new", string(got))
			require.Error(t, Remove(dir, "bob/greet"))
			require.NoError(t, Remove(dir, id))
			assert.NoDirExists(t, actual)
			got, err = os.ReadFile(filepath.Join(dir, "greet", "greet"))
			require.NoError(t, err)
			assert.Equal(t, "other plugin", string(got))
		})
	}
}
