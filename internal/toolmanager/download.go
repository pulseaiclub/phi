package toolmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/util/archive"
	"github.com/pulseaiclub/phi/internal/util/githubrelease"
	platformutil "github.com/pulseaiclub/phi/internal/util/platform"
)

const compatibleReleaseLookback = 10

// BinDir returns the default directory for downloaded tool binaries (~/.phi/bin).
func BinDir() (string, error) {
	return project.GetDefaultProject().Global().BinDir(), nil
}

func findBinaryRecursively(rootDir, binaryFileName string) (string, error) {
	var found string
	err := filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Base(path) == binaryFileName {
			found = path
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil && !errors.Is(err, filepath.SkipDir) {
		return "", err
	}
	return found, nil
}

func findReleaseAsset(assets []githubrelease.Asset, candidates []string) (githubrelease.Asset, bool) {
	assetsByName := make(map[string]githubrelease.Asset, len(assets))
	for _, asset := range assets {
		assetsByName[asset.Name] = asset
	}
	for _, candidate := range candidates {
		if asset, ok := assetsByName[candidate]; ok {
			return asset, true
		}
	}
	return githubrelease.Asset{}, false
}

func selectCompatibleAsset(
	config ToolConfig,
	releases []githubrelease.Release,
	targetPlatform, targetArch string,
) (githubrelease.Asset, error) {
	if len(config.AssetNames.getAssetNames("", targetPlatform, targetArch)) == 0 {
		return githubrelease.Asset{}, fmt.Errorf("unsupported platform: %s/%s", targetPlatform, targetArch)
	}

	checkedTags := make([]string, 0, len(releases))
	for _, release := range releases {
		checkedTags = append(checkedTags, release.TagName)
		version := githubrelease.TagVersion(release.TagName)
		candidates := config.AssetNames.getAssetNames(version, targetPlatform, targetArch)
		asset, ok := findReleaseAsset(release.Assets, candidates)
		if !ok {
			continue
		}
		if asset.BrowserDownloadURL == "" {
			return githubrelease.Asset{}, fmt.Errorf("%s release asset %s has no download URL", config.Name, asset.Name)
		}
		return asset, nil
	}

	return githubrelease.Asset{}, fmt.Errorf(
		"%s has no compatible release asset for %s/%s (checked: %s)",
		config.Name,
		targetPlatform,
		targetArch,
		strings.Join(checkedTags, ", "),
	)
}

// DownloadTool downloads the specified tool from GitHub releases and installs
// it to the phi bin directory (~/.phi/bin/).
func DownloadTool(ctx context.Context, tool string) (string, error) {
	config, ok := Tools[tool]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", tool)
	}

	if platform == "" || arch == "" {
		return "", fmt.Errorf("unsupported platform: %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	releases, err := githubrelease.FetchRecent(ctx, config.Repo, compatibleReleaseLookback)
	if err != nil {
		return "", err
	}

	asset, err := selectCompatibleAsset(config, releases, platform, arch)
	if err != nil {
		return "", err
	}
	assetName := asset.Name
	downloadURL := asset.BrowserDownloadURL

	toolsDir, err := BinDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		return "", fmt.Errorf("create tools directory %q failed: %w", toolsDir, err)
	}

	binaryFileName := platformutil.ExecutableName(config.BinaryName)
	binaryPath := filepath.Join(toolsDir, binaryFileName)
	archivePath := filepath.Join(toolsDir, assetName)

	if err := githubrelease.DownloadFile(ctx, downloadURL, archivePath); err != nil {
		return "", err
	}

	extractDir, err := os.MkdirTemp(toolsDir, "extract_tmp_"+config.BinaryName+"_")
	if err != nil {
		_ = os.Remove(archivePath)
		return "", fmt.Errorf("create extract directory failed: %w", err)
	}

	defer func() {
		_ = os.Remove(archivePath)
		_ = os.RemoveAll(extractDir)
	}()

	switch {
	case strings.HasSuffix(assetName, ".tar.gz"):
		if err := archive.Extract(ctx, archivePath, "tar.gz", extractDir); err != nil {
			return "", fmt.Errorf("failed to extract %s: %w", assetName, err)
		}
	case strings.HasSuffix(assetName, ".zip"):
		if err := archive.Extract(ctx, archivePath, "zip", extractDir); err != nil {
			return "", fmt.Errorf("failed to extract %s: %w", assetName, err)
		}
	default:
		return "", fmt.Errorf("unsupported archive format: %s", assetName)
	}

	extractedDir := filepath.Join(
		extractDir,
		strings.TrimSuffix(strings.TrimSuffix(assetName, ".tar.gz"), ".zip"),
	)
	candidates := []string{
		filepath.Join(extractedDir, binaryFileName),
		filepath.Join(extractDir, binaryFileName),
	}

	extractedBinary := ""
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			extractedBinary = c
			break
		}
	}
	if extractedBinary == "" {
		var findErr error
		extractedBinary, findErr = findBinaryRecursively(extractDir, binaryFileName)
		if findErr != nil {
			return "", fmt.Errorf("failed to search binary in extracted dir: %w", findErr)
		}
	}
	if extractedBinary == "" {
		return "", fmt.Errorf("binary not found in archive: expected %s under %s", binaryFileName, extractDir)
	}

	_ = os.Remove(binaryPath)
	if err := os.Rename(extractedBinary, binaryPath); err != nil {
		return "", fmt.Errorf("move binary to %q failed: %w", binaryPath, err)
	}
	if platform != PlatformWin32 {
		if err := os.Chmod(binaryPath, 0o755); err != nil {
			return "", fmt.Errorf("chmod binary %q failed: %w", binaryPath, err)
		}
	}

	return binaryPath, nil
}
