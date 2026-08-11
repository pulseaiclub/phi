package toolmanager

import (
	"strings"
	"testing"

	"github.com/pulseaiclub/phi/internal/util/githubrelease"
)

func TestFindReleaseAssetUsesCandidateOrder(t *testing.T) {
	t.Parallel()
	assets := []githubrelease.Asset{
		{Name: "tool-gnu.tar.gz", BrowserDownloadURL: "https://example.com/gnu"},
		{Name: "tool-musl.tar.gz", BrowserDownloadURL: "https://example.com/musl"},
	}

	got, ok := findReleaseAsset(assets, []string{"tool-musl.tar.gz", "tool-gnu.tar.gz"})
	if !ok {
		t.Fatal("findReleaseAsset() did not find a compatible asset")
	}
	if got.Name != "tool-musl.tar.gz" {
		t.Fatalf("findReleaseAsset() = %q, want musl candidate", got.Name)
	}
}

func TestFindReleaseAssetFallsBack(t *testing.T) {
	t.Parallel()
	assets := []githubrelease.Asset{
		{Name: "tool-gnu.tar.gz", BrowserDownloadURL: "https://example.com/gnu"},
	}

	got, ok := findReleaseAsset(assets, []string{"tool-musl.tar.gz", "tool-gnu.tar.gz"})
	if !ok {
		t.Fatal("findReleaseAsset() did not use the fallback asset")
	}
	if got.Name != "tool-gnu.tar.gz" {
		t.Fatalf("findReleaseAsset() = %q, want GNU fallback", got.Name)
	}
}

func TestSelectCompatibleAssetFallsBackToOlderRelease(t *testing.T) {
	t.Parallel()
	releases := []githubrelease.Release{
		{
			TagName: "v10.4.2",
			Assets: []githubrelease.Asset{
				{Name: "fd-v10.4.2-aarch64-apple-darwin.tar.gz", BrowserDownloadURL: "https://example.com/v10.4.2-arm64"},
			},
		},
		{
			TagName: "v10.3.0",
			Assets: []githubrelease.Asset{
				{Name: "fd-v10.3.0-x86_64-apple-darwin.tar.gz", BrowserDownloadURL: "https://example.com/v10.3.0-amd64"},
			},
		},
	}

	asset, err := selectCompatibleAsset(
		Tools["fd"],
		releases,
		PlatformDarwin,
		ArchAMD64,
		"darwin",
		"amd64",
	)
	if err != nil {
		t.Fatal(err)
	}
	if asset.Name != "fd-v10.3.0-x86_64-apple-darwin.tar.gz" {
		t.Fatalf("selectCompatibleAsset() = %q, want v10.3.0 Intel macOS asset", asset.Name)
	}
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
		"darwin",
		"amd64",
	)
	if err == nil {
		t.Fatal("selectCompatibleAsset() unexpectedly found an asset")
	}
	for _, want := range []string{"fd has no compatible release asset", "darwin/amd64", "v10.4.2, v10.4.1"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("selectCompatibleAsset() error = %q, want substring %q", err, want)
		}
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
		"linux",
		"amd64",
	)
	if err == nil || !strings.Contains(err.Error(), "has no download URL") {
		t.Fatalf("selectCompatibleAsset() error = %v, want missing URL error", err)
	}
}
