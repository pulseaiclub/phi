// Package githubrelease provides shared helpers for GitHub Releases API and asset downloads.
package githubrelease

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/pulseaiclub/phi/internal/util"
)

const apiVersion = "2022-11-28"

const maxReleasesPerPage = 100

// Asset is a downloadable file attached to a GitHub release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Release is metadata from the GitHub Releases API.
type Release struct {
	TagName    string  `json:"tag_name"`
	HTMLURL    string  `json:"html_url"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

// FetchLatest queries the latest published release for owner/repo.
func FetchLatest(ctx context.Context, repo string) (Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, fmt.Errorf("create request for %q: %w", repo, err)
	}
	req.Header.Set("accept", "application/vnd.github+json")
	req.Header.Set("x-github-api-version", apiVersion)
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := util.DefaultHTTPClient().Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("fetch latest release from %q: %w", repo, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		// GitHub returns 404 both for missing repos and for repos with no
		// published releases. For public pulseaiclub/phi the latter is common
		// before the first tag-triggered GoReleaser run.
		return Release{}, fmt.Errorf("no published release for %s (publish one with ./scripts/bump.sh vX.Y.Z && git push --follow-tags)", repo)
	}
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("github api %s: status %d", repo, resp.StatusCode)
	}

	release, err := decodeRelease(resp.Body)
	if err != nil {
		return Release{}, fmt.Errorf("decode release response from %q: %w", repo, err)
	}
	return release, nil
}

// FetchRecent returns up to limit recent published, non-prerelease releases,
// ordered newest first by the GitHub API.
func FetchRecent(ctx context.Context, repo string, limit int) ([]Release, error) {
	if limit < 1 || limit > maxReleasesPerPage {
		return nil, fmt.Errorf("release limit must be between 1 and %d", maxReleasesPerPage)
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=%d", repo, limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request for %q: %w", repo, err)
	}
	req.Header.Set("accept", "application/vnd.github+json")
	req.Header.Set("x-github-api-version", apiVersion)
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := util.DefaultHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch recent releases from %q: %w", repo, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("no published release for %s", repo)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api %s: status %d", repo, resp.StatusCode)
	}

	releases, err := decodeReleases(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("decode releases response from %q: %w", repo, err)
	}
	releases = stableReleases(releases)
	if len(releases) == 0 {
		return nil, fmt.Errorf("no published stable release for %s", repo)
	}
	return releases, nil
}

func decodeRelease(r io.Reader) (Release, error) {
	var release Release
	if err := json.NewDecoder(r).Decode(&release); err != nil {
		return Release{}, err
	}
	return release, nil
}

func decodeReleases(r io.Reader) ([]Release, error) {
	var releases []Release
	if err := json.NewDecoder(r).Decode(&releases); err != nil {
		return nil, err
	}
	return releases, nil
}

func stableReleases(releases []Release) []Release {
	stable := make([]Release, 0, len(releases))
	for _, release := range releases {
		if release.Draft || release.Prerelease {
			continue
		}
		stable = append(stable, release)
	}
	return stable
}

// TagVersion returns the tag without a leading v/V prefix (for tool asset names).
func TagVersion(tag string) string {
	if len(tag) > 0 && (tag[0] == 'v' || tag[0] == 'V') {
		return tag[1:]
	}
	return tag
}

// DownloadBaseURL converts a release tag page URL to the release download root.
func DownloadBaseURL(htmlURL string) string {
	base := strings.TrimSuffix(htmlURL, "/")
	return strings.Replace(base, "/releases/tag/", "/releases/download/", 1)
}
