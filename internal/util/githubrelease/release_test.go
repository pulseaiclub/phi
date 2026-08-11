package githubrelease

import (
	"strings"
	"testing"
)

func TestTagVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		tag  string
		want string
	}{
		{tag: "v1.2.3", want: "1.2.3"},
		{tag: "V1.2.3", want: "1.2.3"},
		{tag: "1.2.3", want: "1.2.3"},
	}
	for _, tt := range tests {
		if got := TagVersion(tt.tag); got != tt.want {
			t.Fatalf("TagVersion(%q) = %q, want %q", tt.tag, got, tt.want)
		}
	}
}

func TestDownloadBaseURL(t *testing.T) {
	t.Parallel()
	in := "https://github.com/pulseaiclub/phi/releases/tag/v0.1.0"
	want := "https://github.com/pulseaiclub/phi/releases/download/v0.1.0"
	if got := DownloadBaseURL(in); got != want {
		t.Fatalf("DownloadBaseURL() = %q, want %q", got, want)
	}
}

func TestDecodeReleaseIncludesAssets(t *testing.T) {
	t.Parallel()
	response := `{
		"tag_name": "15.2.0",
		"html_url": "https://github.com/BurntSushi/ripgrep/releases/tag/15.2.0",
		"assets": [{
			"name": "ripgrep-15.2.0-x86_64-unknown-linux-musl.tar.gz",
			"browser_download_url": "https://github.com/BurntSushi/ripgrep/releases/download/15.2.0/ripgrep-15.2.0-x86_64-unknown-linux-musl.tar.gz"
		}]
	}`

	release, err := decodeRelease(strings.NewReader(response))
	if err != nil {
		t.Fatal(err)
	}
	if release.TagName != "15.2.0" {
		t.Fatalf("TagName = %q, want 15.2.0", release.TagName)
	}
	if len(release.Assets) != 1 {
		t.Fatalf("len(Assets) = %d, want 1", len(release.Assets))
	}
	asset := release.Assets[0]
	if asset.Name != "ripgrep-15.2.0-x86_64-unknown-linux-musl.tar.gz" {
		t.Fatalf("asset Name = %q", asset.Name)
	}
	if asset.BrowserDownloadURL == "" {
		t.Fatal("asset BrowserDownloadURL is empty")
	}
}

func TestStableReleasesFiltersDraftsAndPrereleases(t *testing.T) {
	t.Parallel()
	response := `[
		{"tag_name": "v3.0.0-rc.1", "prerelease": true},
		{"tag_name": "v2.0.0", "draft": true},
		{"tag_name": "v1.0.0", "assets": [{"name": "tool.tar.gz"}]}
	]`

	releases, err := decodeReleases(strings.NewReader(response))
	if err != nil {
		t.Fatal(err)
	}
	stable := stableReleases(releases)
	if len(stable) != 1 || stable[0].TagName != "v1.0.0" {
		t.Fatalf("stableReleases() = %#v, want only v1.0.0", stable)
	}
}

func TestFetchRecentRejectsInvalidLimit(t *testing.T) {
	t.Parallel()
	for _, limit := range []int{0, maxReleasesPerPage + 1} {
		if _, err := FetchRecent(t.Context(), "owner/repo", limit); err == nil {
			t.Fatalf("FetchRecent(limit=%d) unexpectedly succeeded", limit)
		}
	}
}
