package extension

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/util/githubrelease"
)

// managedPlugin seeds a phi-managed extension dir: phi.yaml, exec binary, and
// install metadata.
func managedPlugin(t *testing.T, dir string, meta InstallMeta, marker string) {
	t.Helper()
	const id = "greet"
	d := filepath.Join(dir, id)
	require.NoError(t, os.MkdirAll(d, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(d, "phi.yaml"), []byte("name: "+id+"\nexec: ./"+id+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(d, id), []byte(marker), 0o755))
	require.NoError(t, writeInstallMeta(d, meta))
}

func pluginTarGzBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}))
		_, err := io.WriteString(tw, body)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func releaseWithAsset(
	t *testing.T,
	tag, repo string,
	files map[string]string,
) (githubrelease.Release, map[string]string) {
	t.Helper()
	assetName, _, err := platformArchiveName(repo, tag)
	require.NoError(t, err)
	assets := map[string]string{assetName: string(pluginTarGzBytes(t, files))}
	rel := githubrelease.Release{
		TagName: tag,
		HTMLURL: "https://github.com/alice/" + repo + "/releases/tag/" + tag,
		Assets:  []githubrelease.Asset{{Name: assetName, BrowserDownloadURL: "https://example.test/" + assetName}},
	}
	return rel, assets
}

func serveDownloads(t *testing.T, assets map[string]string) func(context.Context, string, string) error {
	t.Helper()
	return func(_ context.Context, url, dest string) error {
		body, ok := assets[filepath.Base(url)]
		require.True(t, ok, "unexpected download %s", url)
		return os.WriteFile(dest, []byte(body), 0o644)
	}
}

func TestUpdateToNewerRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tar.gz release path covered on unix CI")
	}
	dir := t.TempDir()
	managedPlugin(t, dir, InstallMeta{
		Spec:        Spec{Owner: "alice", Repo: "greet"},
		Source:      SourceRelease,
		ReleaseTag:  "v1.2.3",
		InstalledAt: time.Now().UTC(),
	}, "#!/bin/sh\n# v1\n")

	rel, assets := releaseWithAsset(t, "v2.0.0", "greet", map[string]string{
		"phi.yaml": "name: greet\nexec: ./greet\n",
		"greet":    "#!/bin/sh\n# v2\n",
	})

	var out bytes.Buffer
	err := Update(t.Context(), UpdateOptions{
		Dir:    dir,
		ID:     "greet",
		Stdout: &out,
		FetchRelease: func(_ context.Context, ownerRepo, ref string) (githubrelease.Release, error) {
			assert.Equal(t, "alice/greet", ownerRepo)
			assert.Empty(t, ref)
			return rel, nil
		},
		DownloadFile: serveDownloads(t, assets),
	})
	require.NoError(t, err)

	got, err := readInstallMeta(filepath.Join(dir, "greet"))
	require.NoError(t, err)
	assert.Equal(t, "v2.0.0", got.ReleaseTag)
	bin, err := os.ReadFile(filepath.Join(dir, "greet", "greet"))
	require.NoError(t, err)
	assert.Contains(t, string(bin), "# v2")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "no leftover backup or clone dirs")
	assert.Equal(t, "greet", entries[0].Name())
}

func TestUpdateUpToDate(t *testing.T) {
	dir := t.TempDir()
	managedPlugin(t, dir, InstallMeta{
		Spec:        Spec{Owner: "alice", Repo: "greet"},
		Source:      SourceRelease,
		ReleaseTag:  "v2.0.0",
		InstalledAt: time.Now().UTC(),
	}, "#!/bin/sh\n# v2\n")

	var out bytes.Buffer
	err := Update(t.Context(), UpdateOptions{
		Dir:    dir,
		ID:     "greet",
		Stdout: &out,
		FetchRelease: func(context.Context, string, string) (githubrelease.Release, error) {
			return githubrelease.Release{TagName: "v2.0.0"}, nil
		},
		DownloadFile: func(context.Context, string, string) error {
			t.Fatal("no download when up to date")
			return nil
		},
	})
	require.NoError(t, err)
	assert.Contains(t, out.String(), "up to date (v2.0.0)")
}

func TestUpdateCheckOnlyDoesNotInstall(t *testing.T) {
	dir := t.TempDir()
	managedPlugin(t, dir, InstallMeta{
		Spec:        Spec{Owner: "alice", Repo: "greet"},
		Source:      SourceRelease,
		ReleaseTag:  "v1.2.3",
		InstalledAt: time.Now().UTC(),
	}, "#!/bin/sh\n# v1\n")

	var out bytes.Buffer
	err := Update(t.Context(), UpdateOptions{
		Dir:    dir,
		ID:     "greet",
		Check:  true,
		Stdout: &out,
		FetchRelease: func(context.Context, string, string) (githubrelease.Release, error) {
			return githubrelease.Release{TagName: "v2.0.0"}, nil
		},
		DownloadFile: func(context.Context, string, string) error {
			t.Fatal("check must not download")
			return nil
		},
	})
	require.NoError(t, err)
	assert.Contains(t, out.String(), "update available -> v2.0.0")

	bin, err := os.ReadFile(filepath.Join(dir, "greet", "greet"))
	require.NoError(t, err)
	assert.Contains(t, string(bin), "# v1")
}

func TestUpdateGitSourceRefreshesClone(t *testing.T) {
	dir := t.TempDir()
	managedPlugin(t, dir, InstallMeta{
		Spec:        Spec{Owner: "alice", Repo: "greet", Ref: "main"},
		Source:      SourceGit,
		InstalledAt: time.Now().UTC(),
	}, "#!/bin/sh\n# old\n")

	var out bytes.Buffer
	err := Update(t.Context(), UpdateOptions{
		Dir:    dir,
		ID:     "greet",
		Git:    "git",
		Stdout: &out,
		FetchRelease: func(context.Context, string, string) (githubrelease.Release, error) {
			return githubrelease.Release{}, fmt.Errorf("no releases")
		},
		RunGit: func(_ context.Context, gitBin string, args ...string) error {
			assert.Equal(t, "git", gitBin)
			assert.Contains(t, args, "--branch")
			assert.Contains(t, args, "main")
			dest := args[len(args)-1]
			require.NoError(t, os.MkdirAll(dest, 0o755))
			require.NoError(
				t,
				os.WriteFile(filepath.Join(dest, "phi.yaml"), []byte("name: greet\nexec: ./greet\n"), 0o644),
			)
			return os.WriteFile(filepath.Join(dest, "greet"), []byte("#!/bin/sh\n# new\n"), 0o755)
		},
	})
	require.NoError(t, err)
	assert.Contains(t, out.String(), "installed alice/greet (main)")

	bin, err := os.ReadFile(filepath.Join(dir, "greet", "greet"))
	require.NoError(t, err)
	assert.Contains(t, string(bin), "# new")
	got, err := readInstallMeta(filepath.Join(dir, "greet"))
	require.NoError(t, err)
	assert.Equal(t, SourceGit, got.Source)
	assert.Equal(t, "main", got.Spec.Ref)
}

func TestUpdateRefOverride(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tar.gz release path covered on unix CI")
	}
	dir := t.TempDir()
	managedPlugin(t, dir, InstallMeta{
		Spec:        Spec{Owner: "alice", Repo: "greet"},
		Source:      SourceRelease,
		ReleaseTag:  "v1.2.3",
		InstalledAt: time.Now().UTC(),
	}, "#!/bin/sh\n# v1\n")

	rel, assets := releaseWithAsset(t, "v2.0.0", "greet", map[string]string{
		"phi.yaml": "name: greet\nexec: ./greet\n",
		"greet":    "#!/bin/sh\n# v2\n",
	})

	err := Update(t.Context(), UpdateOptions{
		Dir: dir,
		ID:  "greet",
		Ref: "v2.0.0",
		FetchRelease: func(_ context.Context, _, ref string) (githubrelease.Release, error) {
			assert.Equal(t, "v2.0.0", ref)
			return rel, nil
		},
		DownloadFile: serveDownloads(t, assets),
	})
	require.NoError(t, err)

	got, err := readInstallMeta(filepath.Join(dir, "greet"))
	require.NoError(t, err)
	assert.Equal(t, "v2.0.0", got.ReleaseTag)
	assert.Equal(t, "v2.0.0", got.Spec.Ref)
}

func TestUpdateUnmanagedRefused(t *testing.T) {
	dir := t.TempDir()
	d := filepath.Join(dir, "greet")
	require.NoError(t, os.MkdirAll(d, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(d, "phi.yaml"), []byte("name: greet\nexec: ./greet\n"), 0o644))

	err := Update(t.Context(), UpdateOptions{Dir: dir, ID: "greet", Stdout: io.Discard})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not installed via 'phi plugin install'")
}

func TestUpdateNotInstalled(t *testing.T) {
	err := Update(t.Context(), UpdateOptions{Dir: t.TempDir(), ID: "ghost"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"ghost" is not installed`)
}

func TestRemoveRegistered(t *testing.T) {
	dir := t.TempDir()
	managedPlugin(t, dir, InstallMeta{
		Spec:        Spec{Owner: "alice", Repo: "greet"},
		Source:      SourceGit,
		InstalledAt: time.Now().UTC(),
	}, "#!/bin/sh\n")

	require.NoError(t, Remove(dir, "greet"))
	_, err := os.Stat(filepath.Join(dir, "greet"))
	assert.True(t, os.IsNotExist(err))
}

func TestRemoveRefusesUnmanaged(t *testing.T) {
	dir := t.TempDir()
	d := filepath.Join(dir, "greet")
	require.NoError(t, os.MkdirAll(d, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(d, "phi.yaml"), []byte("name: greet\nexec: ./greet\n"), 0o644))

	err := Remove(dir, "greet")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not installed via 'phi plugin install'")
	assert.DirExists(t, d)
}

func TestRemoveMissing(t *testing.T) {
	err := Remove(t.TempDir(), "ghost")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"ghost" is not installed`)
}
