package extension

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pulseaiclub/phi/internal/util/githubrelease"
	"github.com/pulseaiclub/phi/internal/util/update"
)

// UpdateOptions configures Update.
type UpdateOptions struct {
	// Dir is the extensions directory (typically ~/.phi/extensions).
	Dir string
	// ID is the plugin to update (repo name or "owner/repo"); empty = all managed.
	ID string
	// Ref, when non-empty, overrides the recorded ref for this update
	// ("latest" means the newest release).
	Ref string
	// Check reports available updates without installing anything.
	Check  bool
	Stdout io.Writer
	// Git/RunGit/FetchRelease/DownloadFile override command plumbing (tests).
	Git          string
	RunGit       func(ctx context.Context, gitBin string, args ...string) error
	FetchRelease func(ctx context.Context, ownerRepo, ref string) (githubrelease.Release, error)
	DownloadFile func(ctx context.Context, url, dest string) error
}

func (o UpdateOptions) out() io.Writer {
	if o.Stdout != nil {
		return o.Stdout
	}
	return io.Discard
}

func (o UpdateOptions) installOptions(spec Spec) InstallOptions {
	return InstallOptions{
		Stdout:       o.Stdout,
		Spec:         spec,
		Git:          o.Git,
		RunGit:       o.RunGit,
		FetchRelease: o.FetchRelease,
		DownloadFile: o.DownloadFile,
	}
}

// Update refreshes managed plugins in Dir to the newest version of their
// recorded GitHub source. With Check it only reports what would change.
// The recorded source ref is re-resolved (a pinned tag stays pinned unless
// overridden via Ref); release archives are preferred, git clone is the
// fallback — the directory is swapped atomically either way.
func Update(ctx context.Context, opts UpdateOptions) error {
	installed, err := ListInstalled(opts.Dir)
	if err != nil {
		return err
	}
	targets, err := matchTarget(installed, opts.ID)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return errors.New("no phi-installed plugins to update")
	}

	var errs []error
	for _, in := range targets {
		if err := updateOne(ctx, opts, in); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", in.ID, err))
		}
	}
	return errors.Join(errs...)
}

// matchTarget narrows installed dirs to the update target. Empty id means all
// managed plugins. Unmanaged dirs are refused: phi never writes into or
// removes directories it does not own.
func matchTarget(installed []Installed, id string) ([]Installed, error) {
	if id == "" {
		var out []Installed
		for _, in := range installed {
			if in.Managed {
				out = append(out, in)
			}
		}
		return out, nil
	}
	var owner string
	if strings.Contains(id, "/") {
		spec, err := ParseSpec(id)
		if err != nil {
			return nil, err
		}
		owner, id = spec.Owner, spec.Repo
	}
	for _, in := range installed {
		matches := in.ID == id
		if owner != "" {
			matches = matches || (in.Meta.Spec.Owner == owner && in.Meta.Spec.Repo == id)
		}
		if !matches {
			continue
		}
		if !in.Managed {
			return nil, fmt.Errorf(
				"%s exists at %s but was not installed via 'phi plugin install' (no %s); remove it manually or reinstall to manage it",
				in.ID,
				in.Path,
				installMetaFile,
			)
		}
		return []Installed{in}, nil
	}
	return nil, fmt.Errorf("plugin %q is not installed", id)
}

func updateOne(ctx context.Context, opts UpdateOptions, in Installed) error {
	spec := in.Meta.Spec
	if opts.Ref != "" {
		spec.Ref = opts.Ref
		if spec.Ref == "latest" {
			spec.Ref = ""
		}
	}
	if opts.Check {
		return reportOne(ctx, opts, in, spec)
	}
	return installOne(ctx, opts, in, spec)
}

// reportOne prints whether an update is available without writing anything.
func reportOne(ctx context.Context, opts UpdateOptions, in Installed, spec Spec) error {
	rel, err := fetchReleaseFor(ctx, opts, spec)
	if err != nil {
		if in.Meta.Source == SourceGit {
			printf(opts.out(), "%s: git source (ref %q) — run 'phi plugin update' to refresh\n", in.ID, spec.Ref)
			return nil
		}
		return fmt.Errorf("query %s: %w", spec.Owner+"/"+spec.Repo, err)
	}
	if in.Meta.Source == SourceRelease && in.Meta.ReleaseTag != "" &&
		!update.VersionLess(in.Meta.ReleaseTag, rel.TagName) {
		printf(opts.out(), "%s: up to date (%s)\n", in.ID, in.Meta.ReleaseTag)
		return nil
	}
	printf(opts.out(), "%s: update available -> %s\n", in.ID, rel.TagName)
	return nil
}

// installOne re-resolves the plugin's GitHub source and atomically swaps the
// directory, skipping when the installed release tag is already current.
func installOne(ctx context.Context, opts UpdateOptions, in Installed, spec Spec) error {
	dest := filepath.Join(opts.Dir, in.ID)
	if _, err := os.Stat(dest); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf(
				"directory %s is gone; reinstall with 'phi plugin install %s/%s'",
				dest,
				spec.Owner,
				spec.Repo,
			)
		}
		return fmt.Errorf("stat %s: %w", dest, err)
	}
	ioOpts := opts.installOptions(spec)

	rel, relErr := fetchReleaseFor(ctx, opts, spec)
	if relErr == nil {
		if in.Meta.Source == SourceRelease && in.Meta.ReleaseTag != "" &&
			!update.VersionLess(in.Meta.ReleaseTag, rel.TagName) {
			printf(opts.out(), "%s: up to date (%s)\n", in.ID, in.Meta.ReleaseTag)
			return nil
		}
		return installReleaseTree(ctx, ioOpts, spec, rel, dest, true)
	}
	printf(opts.out(), "%s: release path unavailable (%v); trying git clone\n", in.ID, relErr)
	if err := installFromGit(ctx, ioOpts, spec, dest, true); err != nil {
		return fmt.Errorf("git clone fallback failed after release error (%w): %w", relErr, err)
	}
	return nil
}

func fetchReleaseFor(ctx context.Context, opts UpdateOptions, spec Spec) (githubrelease.Release, error) {
	fetch := opts.FetchRelease
	if fetch == nil {
		fetch = defaultFetchRelease
	}
	return fetch(ctx, spec.Owner+"/"+spec.Repo, spec.Ref)
}

// Remove deletes a phi-managed extension directory. Unmanaged or missing
// directories are refused so phi never removes user-built plugins.
func Remove(dir, id string) error {
	dest := filepath.Join(dir, id)
	if _, err := readInstallMeta(dest); err != nil {
		if os.IsNotExist(err) {
			if _, statErr := os.Stat(dest); statErr == nil {
				return fmt.Errorf(
					"%s exists at %s but was not installed via 'phi plugin install' (no %s); remove it manually",
					id, dest, installMetaFile,
				)
			}
			return fmt.Errorf("plugin %q is not installed", id)
		}
		return fmt.Errorf("read install metadata for %s: %w", id, err)
	}
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("remove %s: %w", dest, err)
	}
	return nil
}
