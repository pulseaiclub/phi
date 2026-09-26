package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pulseaiclub/phi/internal/extension/manifest"
	"github.com/pulseaiclub/phi/internal/util/githubrelease"
)

// platformArchiveName returns the GoReleaser-style asset name for a project:
//
//	{name}_{version}_{goos}_{goarch}.tar.gz|zip
//
// Version strips a leading v from the tag (same as phi update).
func platformArchiveName(project, tag string) (name, format string, err error) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	switch goos {
	case "linux", "darwin", "windows":
	default:
		return "", "", fmt.Errorf("unsupported OS for plugin install: %s", goos)
	}
	switch goarch {
	case "amd64", "arm64":
	default:
		return "", "", fmt.Errorf("unsupported CPU arch for plugin install: %s", goarch)
	}
	if goos == "windows" && goarch == "arm64" {
		return "", "", errors.New("windows/arm64 plugin archives are not supported")
	}
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	ver := githubrelease.TagVersion(tag)
	if ver == "" {
		return "", "", errors.New("empty version in release tag")
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return "", "", errors.New("empty project name for release asset")
	}
	return fmt.Sprintf("%s_%s_%s_%s.%s", project, ver, goos, goarch, ext), ext, nil
}

// pickPlatformAsset finds the archive for this OS/arch in a release.
// Prefers an exact GoReleaser name using project; falls back to a unique
// asset whose name ends with _{goos}_{goarch}.{tar.gz|zip}.
func pickPlatformAsset(rel githubrelease.Release, project string) (githubrelease.Asset, string, error) {
	want, format, err := platformArchiveName(project, rel.TagName)
	if err != nil {
		return githubrelease.Asset{}, "", err
	}
	for _, a := range rel.Assets {
		if a.Name == want {
			return a, format, nil
		}
	}

	suffix := fmt.Sprintf("_%s_%s.%s", runtime.GOOS, runtime.GOARCH, format)
	var matches []githubrelease.Asset
	for _, a := range rel.Assets {
		if strings.HasSuffix(a.Name, suffix) {
			matches = append(matches, a)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], format, nil
	case 0:
		return githubrelease.Asset{}, "", fmt.Errorf(
			"no release asset for %s/%s (want %s)",
			runtime.GOOS, runtime.GOARCH, want,
		)
	default:
		names := make([]string, len(matches))
		for i, a := range matches {
			names[i] = a.Name
		}
		return githubrelease.Asset{}, "", fmt.Errorf(
			"ambiguous release assets for %s/%s: %s",
			runtime.GOOS, runtime.GOARCH, strings.Join(names, ", "),
		)
	}
}

func findChecksumAsset(rel githubrelease.Release) (githubrelease.Asset, bool) {
	want := "checksums_" + githubrelease.TagVersion(rel.TagName) + ".txt"
	for _, a := range rel.Assets {
		if a.Name == want || a.Name == "checksums.txt" {
			return a, true
		}
	}
	return githubrelease.Asset{}, false
}

func lookupChecksum(path, asset string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read checksums: %w", err)
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[1] == asset {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("checksum for %s not listed in checksums file", asset)
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func extractArchive(ctx context.Context, archive, format, dst string) error {
	switch format {
	case "tar.gz":
		cmd := exec.CommandContext(ctx, "tar", "-xzf", archive, "-C", dst)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("tar: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	case "zip":
		ps := fmt.Sprintf("Expand-Archive -LiteralPath %q -DestinationPath %q -Force", archive, dst)
		cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", ps)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("powershell Expand-Archive: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	default:
		return fmt.Errorf("unknown archive format: %s", format)
	}
}

// unwrapExtractRoot returns the directory that contains phi.yaml.
// If the archive has a single top-level folder and no root manifest, use that folder.
func unwrapExtractRoot(dir string) (string, error) {
	if _, err := manifest.ReadManifest(dir); err == nil {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var sub string
	for _, e := range entries {
		name := e.Name()
		if name == "." || name == ".." {
			continue
		}
		if strings.HasPrefix(name, ".") {
			continue
		}
		if sub != "" {
			return dir, nil // multiple entries; leave as-is
		}
		if !e.IsDir() {
			return dir, nil
		}
		sub = filepath.Join(dir, name)
	}
	if sub == "" {
		return dir, nil
	}
	if _, err := manifest.ReadManifest(sub); err != nil {
		return dir, nil
	}
	return sub, nil
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to install symlink %s", rel)
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
