package download_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/util/githubrelease"
	"github.com/pulseaiclub/phi/internal/util/update/download"
)

func TestArchiveUsesAssetDigest(t *testing.T) {
	dir := t.TempDir()
	assetName := "phi_1.2.3_darwin_arm64.tar.gz"
	payload := []byte("archive")
	digest := sha256.Sum256(payload)
	var downloaded []string

	path, err := download.Archive(t.Context(), download.Options{
		Release: githubrelease.Release{
			TagName: "v1.2.3",
			Assets: []githubrelease.Asset{{
				Name:               assetName,
				BrowserDownloadURL: "https://example.test/" + assetName,
				Digest:             "sha256:" + hex.EncodeToString(digest[:]),
			}},
		},
		AssetName: assetName,
		Dir:       dir,
		DownloadFile: func(_ context.Context, url, dest string) error {
			downloaded = append(downloaded, url)
			return os.WriteFile(dest, payload, 0o600)
		},
	})
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, assetName), path)
	require.Equal(t, []string{"https://example.test/" + assetName}, downloaded)
}

func TestArchiveDownloadsLegacyChecksumsAndArchiveConcurrently(t *testing.T) {
	dir := t.TempDir()
	assetName := "phi_1.2.3_darwin_arm64.tar.gz"
	payload := []byte("archive")
	digest := sha256.Sum256(payload)
	var mu sync.Mutex
	started := make(chan string, 2)
	startedBoth := make(chan struct{})
	var once sync.Once

	downloadFile := func(ctx context.Context, url, dest string) error {
		name := filepath.Base(dest)
		mu.Lock()
		started <- name
		mu.Unlock()
		if len(started) == 2 {
			once.Do(func() { close(startedBoth) })
		}
		select {
		case <-startedBoth:
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
			return errors.New("downloads did not overlap")
		}
		if name == assetName {
			return os.WriteFile(dest, payload, 0o600)
		}
		return os.WriteFile(dest, []byte(hex.EncodeToString(digest[:])+"  "+assetName+"\n"), 0o600)
	}

	_, err := download.Archive(t.Context(), download.Options{
		Release: githubrelease.Release{
			TagName: "v1.2.3",
			HTMLURL: "https://github.com/pulseaiclub/phi/releases/tag/v1.2.3",
			Assets: []githubrelease.Asset{{
				Name:               assetName,
				BrowserDownloadURL: "https://example.test/" + assetName,
			}},
		},
		AssetName:    assetName,
		Dir:          dir,
		DownloadFile: downloadFile,
	})
	require.NoError(t, err)
}

func TestArchiveCancelsArchiveWhenChecksumsAreInvalid(t *testing.T) {
	dir := t.TempDir()
	assetName := "phi_1.2.3_darwin_arm64.tar.gz"
	archiveCanceled := make(chan struct{})

	downloadFile := func(ctx context.Context, _, dest string) error {
		if filepath.Base(dest) != assetName {
			return os.WriteFile(dest, []byte("no matching checksum\n"), 0o600)
		}
		<-ctx.Done()
		close(archiveCanceled)
		return ctx.Err()
	}

	_, err := download.Archive(t.Context(), download.Options{
		Release: githubrelease.Release{
			TagName: "v1.2.3",
			HTMLURL: "https://github.com/pulseaiclub/phi/releases/tag/v1.2.3",
			Assets: []githubrelease.Asset{{
				Name:               assetName,
				BrowserDownloadURL: "https://example.test/" + assetName,
			}},
		},
		AssetName:    assetName,
		Dir:          dir,
		DownloadFile: downloadFile,
	})
	require.ErrorContains(t, err, "checksum for "+assetName)
	select {
	case <-archiveCanceled:
	case <-time.After(time.Second):
		require.Fail(t, "archive download was not canceled")
	}
}
