package toolmanager

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/util/githubrelease"
)

func TestFindReleaseAssetUsesCandidateOrder(t *testing.T) {
	t.Parallel()
	assets := []githubrelease.Asset{
		{Name: "tool-gnu.tar.gz", BrowserDownloadURL: "https://example.com/gnu"},
		{Name: "tool-musl.tar.gz", BrowserDownloadURL: "https://example.com/musl"},
	}

	got, ok := findReleaseAsset(assets, []string{"tool-musl.tar.gz", "tool-gnu.tar.gz"})
	require.True(t, ok, "findReleaseAsset() did not find a compatible asset")
	require.Equal(t, "tool-musl.tar.gz", got.Name, "findReleaseAsset() = %q, want musl candidate")
}

func TestFindReleaseAssetFallsBack(t *testing.T) {
	t.Parallel()
	assets := []githubrelease.Asset{
		{Name: "tool-gnu.tar.gz", BrowserDownloadURL: "https://example.com/gnu"},
	}

	got, ok := findReleaseAsset(assets, []string{"tool-musl.tar.gz", "tool-gnu.tar.gz"})
	require.True(t, ok, "findReleaseAsset() did not use the fallback asset")
	require.Equal(t, "tool-gnu.tar.gz", got.Name, "findReleaseAsset() = %q, want GNU fallback")
}

func TestSelectCompatibleAssetFallsBackToOlderRelease(t *testing.T) {
	t.Parallel()
	releases := []githubrelease.Release{
		{
			TagName: "v10.4.2",
			Assets: []githubrelease.Asset{
				{
					Name:               "fd-v10.4.2-aarch64-apple-darwin.tar.gz",
					BrowserDownloadURL: "https://example.com/v10.4.2-arm64",
				},
			},
		},
		{
			TagName: "v10.3.0",
			Assets: []githubrelease.Asset{
				{
					Name:               "fd-v10.3.0-x86_64-apple-darwin.tar.gz",
					BrowserDownloadURL: "https://example.com/v10.3.0-amd64",
				},
			},
		},
	}

	asset, err := selectCompatibleAsset(
		Tools["fd"],
		releases,
		PlatformDarwin,
		ArchAMD64,
	)
	require.NoError(t, err)
	require.Equal(
		t,
		"fd-v10.3.0-x86_64-apple-darwin.tar.gz",
		asset.Name,
		"selectCompatibleAsset() = %q, want v10.3.0 Intel macOS asset",
	)
}

func TestSelectCompatibleAssetNoMatch(t *testing.T) {
	t.Parallel()
	releases := []githubrelease.Release{
		{TagName: "v10.4.2", Assets: []githubrelease.Asset{{Name: "checksums.txt"}}},
		{TagName: "v10.4.1", Assets: []githubrelease.Asset{{Name: "checksums.txt"}}},
	}
	_, err := selectCompatibleAsset(
		Tools["fd"],
		releases,
		PlatformDarwin,
		ArchAMD64,
	)
	require.Error(t, err, "selectCompatibleAsset() unexpectedly found an asset")
	for _, want := range []string{"fd has no compatible release asset", "darwin/amd64", "v10.4.2, v10.4.1"} {
		require.Contains(t, err.Error(), want, "selectCompatibleAsset() error = %q, want substring %q", err, want)
	}
}

func TestSelectCompatibleAssetRequiresDownloadURL(t *testing.T) {
	t.Parallel()
	releases := []githubrelease.Release{
		{
			TagName: "15.2.0",
			Assets: []githubrelease.Asset{
				{Name: "ripgrep-15.2.0-x86_64-unknown-linux-musl.tar.gz"},
			},
		},
	}
	_, err := selectCompatibleAsset(
		Tools["rg"],
		releases,
		PlatformLinux,
		ArchAMD64,
	)
	require.Error(t, err, "selectCompatibleAsset() error = %v, want missing URL error")
	require.Contains(t, err.Error(), "has no download URL")
}
