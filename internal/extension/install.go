package extension

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pulseaiclub/phi/internal/util/githubrelease"
)

// InstallOptions configures Install.
type InstallOptions struct {
	// Dir is the extensions directory (typically ~/.phi/extensions).
	Dir string
	// Spec is the GitHub plugin to install.
	Spec   Spec
	Stdout io.Writer
	// Git, if set, overrides the git executable lookup (tests).
	Git string
	// RunGit, if set, replaces the real git invocation (tests).
	RunGit func(ctx context.Context, gitBin string, args ...string) error
	// FetchRelease overrides GitHub Releases lookup (tests).
	// ref empty means "latest". Returning an error skips to git clone.
	FetchRelease func(ctx context.Context, ownerRepo, ref string) (githubrelease.Release, error)
	// DownloadFile overrides asset downloads (tests).
	DownloadFile func(ctx context.Context, url, dest string) error
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

// Install installs a GitHub extension into Dir/<repo>/.
// Prefer a platform release archive (same naming as phi update); fall back to
// a shallow git clone when no matching release asset exists.
func Install(ctx context.Context, opts InstallOptions) error {
	if opts.Dir == "" {
		return errors.New("extensions directory is required")
	}
	if opts.Spec.Owner == "" || opts.Spec.Repo == "" {
		return errors.New("invalid plugin spec: missing owner/repo")
	}

	dest := filepath.Join(opts.Dir, opts.Spec.ID())
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("plugin %q already exists at %s (remove it first)", opts.Spec.ID(), dest)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", dest, err)
	}

	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return fmt.Errorf("create extensions dir %s: %w", opts.Dir, err)
	}

	return installInto(ctx, opts, opts.Spec, dest, false)
}

// installInto stages a plugin (release archive first, git clone fallback) and
// places it at dest. replace allows overwriting an existing directory (plugin
// update); install always passes false.
func installInto(ctx context.Context, opts InstallOptions, spec Spec, dest string, replace bool) error {
	relErr := installFromRelease(ctx, opts, spec, dest, replace)
	if relErr == nil {
		return nil
	}
	printf(opts.out(), "plugin: release path unavailable (%v); trying git clone\n", relErr)

	if err := installFromGit(ctx, opts, spec, dest, replace); err != nil {
		return fmt.Errorf("git clone fallback failed after release error (%w): %w", relErr, err)
	}
	return nil
}

func installFromRelease(ctx context.Context, opts InstallOptions, spec Spec, dest string, replace bool) error {
	ownerRepo := spec.Owner + "/" + spec.Repo
	fetch := opts.FetchRelease
	if fetch == nil {
		fetch = defaultFetchRelease
	}
	rel, err := fetch(ctx, ownerRepo, spec.Ref)
	if err != nil {
		return err
	}
	return installReleaseTree(ctx, opts, spec, rel, dest, replace)
}

// installReleaseTree downloads/verifies/extracts a release archive, stages it,
// records install metadata, and places it at dest.
func installReleaseTree(
	ctx context.Context,
	opts InstallOptions,
	spec Spec,
	rel githubrelease.Release,
	dest string,
	replace bool,
) error {
	asset, format, err := pickPlatformAsset(rel, spec.Repo)
	if err != nil {
		return err
	}
	if asset.BrowserDownloadURL == "" {
		return fmt.Errorf("release asset %q has no download URL", asset.Name)
	}

	tmp, err := os.MkdirTemp("", "phi-plugin-")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	download := opts.DownloadFile
	if download == nil {
		download = githubrelease.DownloadFile
	}

	printf(opts.out(), "plugin: downloading %s (%s)\n", asset.Name, rel.TagName)
	archivePath := filepath.Join(tmp, asset.Name)
	if err := download(ctx, asset.BrowserDownloadURL, archivePath); err != nil {
		return fmt.Errorf("download %s: %w", asset.Name, err)
	}

	if sumAsset, ok := findChecksumAsset(rel); ok && sumAsset.BrowserDownloadURL != "" {
		printf(opts.out(), "plugin: verifying checksum...\n")
		sumsPath := filepath.Join(tmp, sumAsset.Name)
		if err := download(ctx, sumAsset.BrowserDownloadURL, sumsPath); err != nil {
			return fmt.Errorf("download checksums: %w", err)
		}
		want, err := lookupChecksum(sumsPath, asset.Name)
		if err != nil {
			return err
		}
		got, err := sha256File(archivePath)
		if err != nil {
			return fmt.Errorf("hash archive: %w", err)
		}
		if !strings.EqualFold(got, want) {
			return fmt.Errorf("checksum mismatch for %s: got %s, want %s", asset.Name, got, want)
		}
	}

	extractDir := filepath.Join(tmp, "extracted")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return fmt.Errorf("mkdir extract: %w", err)
	}
	printf(opts.out(), "plugin: extracting...\n")
	if err := extractArchive(ctx, archivePath, format, extractDir); err != nil {
		return fmt.Errorf("extract archive: %w", err)
	}
	root, err := unwrapExtractRoot(extractDir)
	if err != nil {
		return err
	}

	staging := filepath.Join(tmp, "staging")
	if err := copyTree(root, staging); err != nil {
		return fmt.Errorf("stage extracted files: %w", err)
	}
	entry, err := findInstallEntry(staging)
	if err != nil {
		return err
	}
	if err := ensureExecMode(entry); err != nil {
		return err
	}
	if err := writeInstallMeta(staging, InstallMeta{
		Spec:        spec,
		Source:      SourceRelease,
		ReleaseTag:  rel.TagName,
		InstalledAt: time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("write install metadata: %w", err)
	}

	if err := placeTree(staging, dest, replace); err != nil {
		return err
	}

	finalEntry, err := findInstallEntry(dest)
	if err != nil {
		if !replace {
			_ = os.RemoveAll(dest)
		}
		return err
	}

	refNote := rel.TagName
	printf(opts.out(), "installed %s/%s (%s) → %s\n", spec.Owner, spec.Repo, refNote, dest)
	printf(opts.out(), "entry: %s\n", finalEntry)
	printf(opts.out(), "source: release asset %s\n", asset.Name)
	printInstallWarnings(opts.out())
	return nil
}

func ensureExecMode(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	return os.Chmod(path, 0o755)
}

func defaultFetchRelease(ctx context.Context, ownerRepo, ref string) (githubrelease.Release, error) {
	if ref == "" {
		return githubrelease.FetchLatest(ctx, ownerRepo)
	}
	return githubrelease.FetchTag(ctx, ownerRepo, ref)
}

func installFromGit(ctx context.Context, opts InstallOptions, spec Spec, dest string, replace bool) error {
	gitBin := opts.Git
	if gitBin == "" {
		var err error
		gitBin, err = exec.LookPath("git")
		if err != nil {
			return errors.New("git not found in PATH (required to install plugins without a release)")
		}
	}

	// Clone into a temp sibling of dest so an update can swap directories
	// atomically on the same volume (no window where dest is missing).
	tmp, err := os.MkdirTemp(filepath.Dir(dest), ".phi-clone-")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	cloneDest := filepath.Join(tmp, "clone")

	args := []string{"clone", "--depth", "1"}
	if spec.Ref != "" {
		args = append(args, "--branch", spec.Ref)
	}
	args = append(args, spec.CloneURL(), cloneDest)

	run := opts.RunGit
	if run == nil {
		run = defaultRunGit
	}
	if err := run(ctx, gitBin, args...); err != nil {
		return err
	}

	if _, err := findInstallEntry(cloneDest); err != nil {
		return err
	}
	if err := writeInstallMeta(cloneDest, InstallMeta{
		Spec:        spec,
		Source:      SourceGit,
		InstalledAt: time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("write install metadata: %w", err)
	}
	if err := placeTree(cloneDest, dest, replace); err != nil {
		return err
	}

	finalEntry, err := findInstallEntry(dest)
	if err != nil {
		if !replace {
			_ = os.RemoveAll(dest)
		}
		return err
	}

	refNote := "default branch"
	if spec.Ref != "" {
		refNote = spec.Ref
	}
	printf(opts.out(), "installed %s/%s (%s) → %s\n", spec.Owner, spec.Repo, refNote, dest)
	printf(opts.out(), "entry: %s\n", finalEntry)
	printf(opts.out(), "source: git clone\n")
	printInstallWarnings(opts.out())
	return nil
}

// placeTree moves a staged tree into dest. When replace is set, dest is moved
// aside first so it is never half-written, then the backup is removed.
func placeTree(staging, dest string, replace bool) error {
	if !replace {
		if err := os.Rename(staging, dest); err != nil {
			// Cross-device rename: copy into place.
			if err2 := copyTree(staging, dest); err2 != nil {
				_ = os.RemoveAll(dest)
				return fmt.Errorf("install to %s: rename: %w; copy: %w", dest, err, err2)
			}
		}
		return nil
	}
	bak := filepath.Join(filepath.Dir(dest), "."+filepath.Base(dest)+".old")
	_ = os.RemoveAll(bak)
	if err := os.Rename(dest, bak); err != nil {
		return fmt.Errorf("move aside %s: %w", dest, err)
	}
	if err := os.Rename(staging, dest); err != nil {
		if err2 := copyTree(staging, dest); err2 != nil {
			_ = os.Rename(bak, dest) // best-effort restore
			return fmt.Errorf("install new version to %s: rename: %w; copy: %w", dest, err, err2)
		}
	}
	if err := os.RemoveAll(bak); err != nil {
		return fmt.Errorf("installed %s but could not remove backup %s: %w", dest, bak, err)
	}
	return nil
}

func printInstallWarnings(w io.Writer) {
	printf(w, "warning: extensions run with your full process permissions; only install from sources you trust\n")
	printf(w, "reload: Ctrl+K → extensions → reload (or restart phi)\n")
}

func defaultRunGit(ctx context.Context, gitBin string, args ...string) error {
	cmd := exec.CommandContext(ctx, gitBin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return nil
}
