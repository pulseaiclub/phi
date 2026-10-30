// Package download fetches and verifies phi release archives.
package download

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/pulseaiclub/phi/internal/util/githubrelease"
)

// Options configures an archive download.
type Options struct {
	Release   githubrelease.Release
	AssetName string
	Dir       string
	Stdout    io.Writer

	// DownloadFile overrides the network download for tests.
	DownloadFile func(ctx context.Context, url, dest string) error
}

func (o Options) out() io.Writer {
	if o.Stdout != nil {
		return o.Stdout
	}
	return io.Discard
}

func printf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

type result struct {
	name string
	err  error
	want string
}

// Archive downloads and verifies the release asset, returning its local path.
// New GitHub releases use the asset digest directly. Older releases without a
// digest download and parse GoReleaser checksums concurrently with the archive.
func Archive(ctx context.Context, opts Options) (string, error) {
	asset, ok := findAsset(opts.Release.Assets, opts.AssetName)
	if !ok {
		return "", fmt.Errorf("release %s has no asset %q", opts.Release.TagName, opts.AssetName)
	}
	if asset.BrowserDownloadURL == "" {
		return "", fmt.Errorf("release asset %q has no download URL", opts.AssetName)
	}

	download := opts.DownloadFile
	if download == nil {
		download = githubrelease.DownloadFile
	}
	archivePath := filepath.Join(opts.Dir, opts.AssetName)
	if digest := sha256Digest(asset.Digest); digest != "" {
		printf(opts.out(), "phi update: downloading archive...\n")
		if err := download(ctx, asset.BrowserDownloadURL, archivePath); err != nil {
			return "", fmt.Errorf("download archive: %w", err)
		}
		if err := verify(archivePath, opts.AssetName, digest); err != nil {
			return "", err
		}
		return archivePath, nil
	}

	base := githubrelease.DownloadBaseURL(opts.Release.HTMLURL)
	sumsName := "checksums_" + githubrelease.TagVersion(opts.Release.TagName) + ".txt"
	sumsPath := filepath.Join(opts.Dir, sumsName)
	printf(opts.out(), "phi update: downloading checksums and archive...\n")

	downloadCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan result, 2)
	go func() {
		res := result{name: "checksums"}
		res.err = download(downloadCtx, base+"/"+sumsName, sumsPath)
		if res.err == nil {
			res.want, res.err = githubrelease.LookupChecksum(sumsPath, opts.AssetName)
		}
		results <- res
	}()
	go func() {
		results <- result{
			name: "archive",
			err:  download(downloadCtx, asset.BrowserDownloadURL, archivePath),
		}
	}()

	var firstErr error
	var want string
	for range 2 {
		res := <-results
		if res.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("download %s: %w", res.name, res.err)
			cancel()
		}
		if res.want != "" {
			want = res.want
		}
	}
	if firstErr != nil {
		return "", firstErr
	}
	if err := verify(archivePath, opts.AssetName, want); err != nil {
		return "", err
	}
	return archivePath, nil
}

func verify(path, assetName, want string) error {
	got, err := githubrelease.SHA256File(path)
	if err != nil {
		return fmt.Errorf("hash archive: %w", err)
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", assetName, got, want)
	}
	return nil
}

func findAsset(assets []githubrelease.Asset, name string) (githubrelease.Asset, bool) {
	for _, asset := range assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return githubrelease.Asset{}, false
}

func sha256Digest(digest string) string {
	const prefix = "sha256:"
	if strings.HasPrefix(strings.ToLower(digest), prefix) {
		return digest[len(prefix):]
	}
	return ""
}
