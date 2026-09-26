package plugin

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/util/githubrelease"
)

func TestPlatformArchiveName(t *testing.T) {
	t.Parallel()
	name, format, err := platformArchiveName("greet", "v1.2.3")
	require.NoError(t, err)
	wantFmt := "tar.gz"
	if runtime.GOOS == "windows" {
		wantFmt = "zip"
	}
	assert.Equal(t, wantFmt, format)
	assert.Equal(t, fmt.Sprintf("greet_1.2.3_%s_%s.%s", runtime.GOOS, runtime.GOARCH, wantFmt), name)
}

func TestPickPlatformAssetExact(t *testing.T) {
	t.Parallel()
	want, _, err := platformArchiveName("greet", "v1.0.0")
	require.NoError(t, err)
	rel := githubrelease.Release{
		TagName: "v1.0.0",
		Assets: []githubrelease.Asset{
			{Name: "other.tar.gz"},
			{Name: want, BrowserDownloadURL: "https://example.test/" + want},
		},
	}
	got, format, err := pickPlatformAsset(rel, "greet")
	require.NoError(t, err)
	assert.Equal(t, want, got.Name)
	assert.NotEmpty(t, format)
}

func TestPickPlatformAssetSuffixFallback(t *testing.T) {
	t.Parallel()
	_, format, err := platformArchiveName("greet", "v1.0.0")
	require.NoError(t, err)
	suffix := fmt.Sprintf("_%s_%s.%s", runtime.GOOS, runtime.GOARCH, format)
	rel := githubrelease.Release{
		TagName: "v1.0.0",
		Assets: []githubrelease.Asset{
			{Name: "myplugin" + suffix, BrowserDownloadURL: "https://example.test/a"},
		},
	}
	got, _, err := pickPlatformAsset(rel, "greet")
	require.NoError(t, err)
	assert.Equal(t, "myplugin"+suffix, got.Name)
}
