package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pulseaiclub/phi/internal/util/archive"
	"github.com/pulseaiclub/phi/internal/util/githubrelease"
)

// InstallOptions configures Install.
type InstallOptions struct {
	Current string // running version string
	Stdout  io.Writer
}

func (o InstallOptions) out() io.Writer {
	if o.Stdout != nil {
		return o.Stdout
	}
	return io.Discard
}

func printf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// CheckOnly prints whether an update is available (no download).
func CheckOnly(ctx context.Context, current string) error {
	if IsDevBuild(current) {
		printf(os.Stdout, "phi: dev build — `phi update` is disabled\n")
		return nil
	}
	rel, err := githubrelease.FetchLatest(ctx, Repo)
	if err != nil {
		return fmt.Errorf("query latest release: %w", err)
	}
	cur := versionOnly(current)
	if !VersionLess(cur, rel.TagName) {
		printf(os.Stdout, "phi %s is up to date (latest: %s)\n",
			strings.TrimPrefix(cur, "v"), strings.TrimPrefix(rel.TagName, "v"))
		return nil
	}
	printf(os.Stdout, "phi %s -> %s available\n  release: %s\n  run 'phi update' to install\n",
		strings.TrimPrefix(cur, "v"), strings.TrimPrefix(rel.TagName, "v"), rel.HTMLURL)
	return nil
}

// Install downloads the latest release archive, verifies its checksum,
// extracts it, and replaces the running binary.
func Install(ctx context.Context, opts InstallOptions) error {
	out := opts.out()
	if IsDevBuild(opts.Current) {
		return errors.New(
			"dev build: `phi update` is disabled. Build a release tag or download from https://github.com/pulseaiclub/phi/releases",
		)
	}
	cur := versionOnly(opts.Current)

	printf(out, "phi update: querying latest release...\n")
	rel, err := githubrelease.FetchLatest(ctx, Repo)
	if err != nil {
		return fmt.Errorf("query latest release: %w", err)
	}

	if !VersionLess(cur, rel.TagName) {
		printf(out, "phi %s is already up to date.\n", strings.TrimPrefix(cur, "v"))
		return nil
	}
	printf(out, "phi update: %s -> %s\n", strings.TrimPrefix(cur, "v"), strings.TrimPrefix(rel.TagName, "v"))
	printf(out, "phi update: release page %s\n", rel.HTMLURL)

	assetName, archiveFmt, err := releaseAssetName(rel.TagName)
	if err != nil {
		return err
	}
	printf(out, "phi update: target asset %s\n", assetName)

	base := githubrelease.DownloadBaseURL(rel.HTMLURL)
	assetURL := base + "/" + assetName
	sumsName := "checksums_" + githubrelease.TagVersion(rel.TagName) + ".txt"
	sumsURL := base + "/" + sumsName

	// Resolve the install path before staging so the temp dir prefers the same
	// volume as the binary (Windows os.Rename cannot cross drives).
	curBin, err := currentBinaryPath()
	if err != nil {
		return err
	}

	tmp, err := stagingDir(curBin)
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	printf(out, "phi update: downloading checksums...\n")
	sumsPath := filepath.Join(tmp, sumsName)
	if err = githubrelease.DownloadFile(ctx, sumsURL, sumsPath); err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	wantSum, err := githubrelease.LookupChecksum(sumsPath, assetName)
	if err != nil {
		return err
	}

	printf(out, "phi update: downloading archive...\n")
	archivePath := filepath.Join(tmp, assetName)
	if err = githubrelease.DownloadFile(ctx, assetURL, archivePath); err != nil {
		return fmt.Errorf("download archive: %w", err)
	}

	printf(out, "phi update: verifying checksum...\n")
	gotSum, err := githubrelease.SHA256File(archivePath)
	if err != nil {
		return fmt.Errorf("hash archive: %w", err)
	}
	if !strings.EqualFold(gotSum, wantSum) {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", assetName, gotSum, wantSum)
	}

	printf(out, "phi update: extracting...\n")
	extractDir := filepath.Join(tmp, "extracted")
	if err = os.MkdirAll(extractDir, 0o755); err != nil {
		return fmt.Errorf("mkdir extract: %w", err)
	}
	if err = archive.Extract(ctx, archivePath, archiveFmt, extractDir); err != nil {
		return fmt.Errorf("extract archive: %w", err)
	}

	newBin := filepath.Join(extractDir, "phi")
	if runtime.GOOS == "windows" {
		newBin = filepath.Join(extractDir, "phi.exe")
	}
	if st, err2 := os.Stat(newBin); err2 != nil || st.IsDir() {
		return fmt.Errorf("extracted archive does not contain a phi binary at %s", newBin)
	}

	printf(out, "phi update: replacing %s\n", curBin)
	if err := replaceBinary(curBin, newBin); err != nil {
		return fmt.Errorf("replace binary: %w", err)
	}
	printf(out, "phi update: installed %s\n", strings.TrimPrefix(rel.TagName, "v"))
	return nil
}

func currentBinaryPath() (string, error) {
	curBin, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve current binary path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(curBin); err == nil {
		curBin = resolved
	}
	return curBin, nil
}

// stagingDir creates a temp directory, preferring the binary's parent so the
// extracted exe and install path share a volume (rename-friendly on Windows).
func stagingDir(binPath string) (string, error) {
	if dir := filepath.Dir(binPath); dir != "" && dir != "." {
		if tmp, err := os.MkdirTemp(dir, "phi-update-"); err == nil {
			return tmp, nil
		}
	}
	return os.MkdirTemp("", "phi-update-")
}

// DefaultInstallTimeout is the network budget for a full self-update.
const DefaultInstallTimeout = 120 * time.Second

// releaseAssetName returns the archive filename for this platform.
// Must stay in sync with name_template in .goreleaser.yaml:
//
//	{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}
//
// GoReleaser's .Version strips the leading v from the tag.
func releaseAssetName(tag string) (name, format string, err error) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	switch goos {
	case "linux", "darwin", "windows":
	default:
		return "", "", fmt.Errorf("unsupported OS for update: %s (download manually from the release page)", goos)
	}
	switch goarch {
	case "amd64", "arm64":
	default:
		return "", "", fmt.Errorf("unsupported CPU arch for update: %s (download manually)", goarch)
	}
	if goos == "windows" && goarch == "arm64" {
		return "", "", errors.New("windows/arm64 builds are not published; download manually if available")
	}
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	ver := githubrelease.TagVersion(tag)
	return fmt.Sprintf("phi_%s_%s_%s.%s", ver, goos, goarch, ext), ext, nil
}

func replaceBinary(cur, newBin string) error {
	info, err := os.Stat(cur)
	if err != nil {
		return fmt.Errorf("stat current binary: %w", err)
	}
	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0o755
	}

	if runtime.GOOS == "windows" {
		// Running Windows binaries cannot be overwritten in place; move the
		// live exe aside first, then install. os.Rename cannot cross volumes
		// (temp is often on C: while the install lives on another drive).
		bak := cur + ".old"
		_ = os.Remove(bak)
		if err := os.Rename(cur, bak); err != nil {
			return fmt.Errorf("rename current to .old: %w", err)
		}
		if err := relocateFile(newBin, cur); err != nil {
			_ = os.Rename(bak, cur)
			return fmt.Errorf("install new binary: %w", err)
		}
		return nil
	}

	if err := relocateFile(newBin, cur); err != nil {
		return fmt.Errorf("copy new binary into place: %w", err)
	}
	_ = os.Chmod(cur, mode)
	return nil
}

// relocateFile moves src to dst, falling back to copy+rename when rename fails
// (cross-volume on Windows, EXDEV on Unix). Staging next to the binary usually
// makes rename succeed; the fallback covers system-temp installs.
func relocateFile(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	if err2 := copyFile(src, dst); err2 != nil {
		return fmt.Errorf("rename: %w; copy: %w", err, err2)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// Same-volume rename into place (dst may be missing after Windows .old aside).
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
