package update

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/pulseaiclub/phi/internal/util/githubrelease"
)

// Repo is the GitHub repository that publishes phi releases.
const Repo = "pulseaiclub/phi"

const (
	updateCheckTTL  = 12 * time.Hour
	updateCheckFile = "update-check.json"
)

// Info describes the result of an update check.
type Info struct {
	Current   string // e.g. "v0.1.0"
	Latest    string // e.g. "v0.2.0"
	Available bool   // true when latest > current
	URL       string // release page URL
}

type updateCache struct {
	CheckedAt time.Time `json:"checked_at"`
	CurrentAt string    `json:"current_at"`
	Latest    string    `json:"latest"`
	URL       string    `json:"url"`
}

// CheckOptions configures Check.
type CheckOptions struct {
	// Current is the running binary version (e.g. version.Version).
	Current string
	// CacheDir is where update-check.json is stored (typically ~/.phi).
	CacheDir string
	// Force bypasses the on-disk cache.
	Force bool
}

// SkipCheck reports whether env vars disable network version checks.
func SkipCheck() bool {
	return os.Getenv("PHI_SKIP_VERSION_CHECK") != ""
}

// Check returns info about a newer release, using a cached result when fresh.
// On network/API failure it returns a zero Info so callers never block UI startup.
func Check(ctx context.Context, opts CheckOptions) Info {
	if SkipCheck() || IsDevBuild(opts.Current) {
		return Info{Current: opts.Current}
	}

	cachePath := ""
	if opts.CacheDir != "" {
		cachePath = filepath.Join(opts.CacheDir, updateCheckFile)
	}

	if !opts.Force && cachePath != "" {
		if c, ok := readUpdateCache(cachePath); ok {
			if time.Since(c.CheckedAt) < updateCheckTTL && c.CurrentAt == opts.Current {
				info := buildInfo(c.CurrentAt, c.Latest, c.URL)
				if info.Available {
					return info
				}
				// Cache says up-to-date; fall through to re-check so a
				// release published moments ago is picked up quickly.
			}
		}
	}

	rel, err := githubrelease.FetchLatest(ctx, Repo)
	if err != nil {
		return Info{Current: opts.Current}
	}

	if cachePath != "" {
		_ = writeUpdateCache(cachePath, updateCache{
			CheckedAt: time.Now().UTC(),
			CurrentAt: opts.Current,
			Latest:    rel.TagName,
			URL:       rel.HTMLURL,
		})
	}
	return buildInfo(opts.Current, rel.TagName, rel.HTMLURL)
}

// CheckAsync runs Check in a goroutine and delivers the result once.
func CheckAsync(opts CheckOptions) <-chan Info {
	ch := make(chan Info, 1)
	go func() {
		defer close(ch)
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		ch <- Check(ctx, opts)
	}()
	return ch
}

func buildInfo(current, latest, url string) Info {
	info := Info{
		Current: current,
		Latest:  latest,
		URL:     url,
	}
	info.Available = VersionLess(versionOnly(current), versionOnly(latest))
	return info
}

func readUpdateCache(path string) (updateCache, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return updateCache{}, false
	}
	var c updateCache
	if err := json.Unmarshal(b, &c); err != nil {
		return updateCache{}, false
	}
	return c, true
}

func writeUpdateCache(path string, c updateCache) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}
