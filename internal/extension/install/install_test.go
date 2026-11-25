package install

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/extension/loader"
	"github.com/pulseaiclub/phi/internal/util/githubrelease"
)

func TestInstallClonesWhenReleaseUnavailable(t *testing.T) {
	dir := t.TempDir()
	spec := Spec{Owner: "alice", Repo: "greet", Ref: "v1"}
	var sawArgs []string
	err := Install(t.Context(), InstallOptions{
		Dir:  dir,
		Spec: spec,
		Git:  "git",
		FetchRelease: func(context.Context, string, string) (githubrelease.Release, error) {
			return githubrelease.Release{}, fmt.Errorf("no published release")
		},
		RunGit: func(_ context.Context, gitBin string, args ...string) error {
			assert.Equal(t, "git", gitBin)
			sawArgs = append([]string{}, args...)
			dest := args[len(args)-1]
			require.NoError(t, os.MkdirAll(dest, 0o755))
			require.NoError(
				t,
				os.WriteFile(filepath.Join(dest, "phi.yaml"), []byte("name: greet\nexec: ./greet\n"), 0o644),
			)
			return os.WriteFile(filepath.Join(dest, "greet"), []byte("#!/bin/true\n"), 0o755)
		},
	})
	require.NoError(t, err)
	// git clones into a temp sibling of the extensions dir, then the staged
	// tree is swapped into place (same volume, atomic update support).
	require.Len(t, sawArgs, 7)
	assert.Equal(t, []string{"clone", "--depth", "1", "--branch", "v1", spec.CloneURL()}, sawArgs[:6])
	assert.True(
		t,
		strings.HasPrefix(sawArgs[6], filepath.Join(dir, ".phi-clone-")),
		"clone into temp sibling of %s, got %s",
		dir, sawArgs[6],
	)

	found, _, err := loader.Discover(dir, "")
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, "greet", found[0].ID)
}

func TestInstallFromReleaseArchive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tar.gz release path covered on unix CI")
	}
	dir := t.TempDir()
	assetName := fmt.Sprintf("greet_1.2.3_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	archivePath := filepath.Join(t.TempDir(), assetName)
	require.NoError(t, writePluginTarGz(archivePath, map[string]string{
		"phi.yaml": "name: greet\nexec: ./greet\n",
		"greet":    "#!/bin/true\n",
	}))
	sum := mustSHA256(t, archivePath)
	sumsBody := sum + "  " + assetName + "\n"
	sumsName := "checksums_1.2.3.txt"

	files := map[string]string{
		assetName: string(mustRead(t, archivePath)),
		sumsName:  sumsBody,
	}

	err := Install(t.Context(), InstallOptions{
		Dir:  dir,
		Spec: Spec{Owner: "alice", Repo: "greet", Ref: "v1.2.3"},
		FetchRelease: func(_ context.Context, ownerRepo, ref string) (githubrelease.Release, error) {
			assert.Equal(t, "alice/greet", ownerRepo)
			assert.Equal(t, "v1.2.3", ref)
			return githubrelease.Release{
				TagName: "v1.2.3",
				HTMLURL: "https://github.com/alice/greet/releases/tag/v1.2.3",
				Assets: []githubrelease.Asset{
					{Name: assetName, BrowserDownloadURL: "https://example.test/" + assetName},
					{Name: sumsName, BrowserDownloadURL: "https://example.test/" + sumsName},
				},
			}, nil
		},
		DownloadFile: func(_ context.Context, url, dest string) error {
			name := filepath.Base(url)
			body, ok := files[name]
			require.True(t, ok, "unexpected download %s", url)
			require.NoError(t, os.MkdirAll(filepath.Dir(dest), 0o755))
			return os.WriteFile(dest, []byte(body), 0o644)
		},
		RunGit: func(context.Context, string, ...string) error {
			require.Fail(t, "git should not run when release succeeds")
			return nil
		},
	})
	require.NoError(t, err)

	found, _, err := loader.Discover(dir, "")
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, "greet", found[0].ID)
	assert.FileExists(t, filepath.Join(dir, "greet", "greet"))
}

func TestInstallRejectsMissingEntry(t *testing.T) {
	dir := t.TempDir()
	err := Install(t.Context(), InstallOptions{
		Dir:  dir,
		Spec: Spec{Owner: "alice", Repo: "empty"},
		Git:  "git",
		FetchRelease: func(context.Context, string, string) (githubrelease.Release, error) {
			return githubrelease.Release{}, fmt.Errorf("no release")
		},
		RunGit: func(_ context.Context, _ string, args ...string) error {
			dest := args[len(args)-1]
			return os.MkdirAll(dest, 0o755)
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "phi.yaml")
	_, err = os.Stat(filepath.Join(dir, "empty"))
	assert.True(t, os.IsNotExist(err), "failed install should clean up dest")
}

func TestInstallRejectsExisting(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "greet")
	require.NoError(t, os.Mkdir(dest, 0o755))
	err := Install(t.Context(), InstallOptions{
		Dir:  dir,
		Spec: Spec{Owner: "alice", Repo: "greet"},
		Git:  "git",
		FetchRelease: func(context.Context, string, string) (githubrelease.Release, error) {
			require.Fail(t, "release should not be queried when dest exists")
			return githubrelease.Release{}, nil
		},
		RunGit: func(context.Context, string, ...string) error {
			require.Fail(t, "git should not run")
			return nil
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

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

func writePluginTarGz(path string, files map[string]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	for name, body := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o755,
			Size: int64(len(body)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := io.WriteString(tw, body); err != nil {
			return err
		}
	}
	return nil
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}

func mustSHA256(t *testing.T, path string) string {
	t.Helper()
	b := mustRead(t, path)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
