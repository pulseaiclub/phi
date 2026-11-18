package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/toolmanager"
)

const bootstrapDownloadTimeout = 5 * time.Minute

// runBootstrap is the shared startup state for headless entrypoints:
// Discover → config → search tools → gate → session dir.
type runBootstrap struct {
	Proj       *project.Project
	Config     *project.Config
	Cwd        string
	SessionDir string
}

// loadRunBootstrap wires the shared startup path used by `phi run` (and any
// future headless subcommand). Search-tool install failures are non-fatal warnings.
func loadRunBootstrap(ctx context.Context, sessionDirOverride string) (*runBootstrap, error) {
	proj := project.GetDefaultProject()
	if err := proj.LoadConfig(); err != nil {
		return nil, err
	}
	if err := EnsureSearchTools(ctx, proj); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not install search tools:", err)
	}
	cwd, _ := os.Getwd()
	sessionDir := sessionDirOverride
	if sessionDir == "" {
		sessionDir = proj.SessionDir()
	}
	return &runBootstrap{
		Proj:       proj,
		Config:     proj.Config(),
		Cwd:        cwd,
		SessionDir: sessionDir,
	}, nil
}

// EnsureSearchTools installs fd and ripgrep into the phi bin dir
// (~/.phi/bin) when they are missing from both the bin dir and PATH.
// Failures are non-fatal: the search tools fall back to PATH at runtime
// and report a clear error if truly unavailable.
func EnsureSearchTools(ctx context.Context, proj *project.Project) error {
	return ensureSearchTools(ctx, proj, toolmanager.DownloadTool)
}

type searchToolDownloader func(context.Context, string) (string, error)

func ensureSearchTools(ctx context.Context, proj *project.Project, download searchToolDownloader) error {
	type downloadResult struct {
		index int
		err   error
	}

	tools := []string{"fd", "rg"}
	results := make(chan downloadResult, len(tools))
	scheduled := 0
	installErrors := make([]error, len(tools))
	for index, tool := range tools {
		if !shouldBootstrap(proj, tool) {
			continue
		}
		scheduled++
		go func(index int, tool string) {
			dlCtx, cancel := context.WithTimeout(ctx, bootstrapDownloadTimeout)
			defer cancel()
			_, err := download(dlCtx, tool)
			if err != nil {
				err = fmt.Errorf("%s: %w", tool, err)
			}
			results <- downloadResult{index: index, err: err}
		}(index, tool)
	}

	for range scheduled {
		result := <-results
		installErrors[result.index] = result.err
	}

	joinedErrors := installErrors[:0]
	for _, err := range installErrors {
		if err != nil {
			joinedErrors = append(joinedErrors, err)
		}
	}
	return errors.Join(joinedErrors...)
}

// shouldBootstrap is true when the tool binary is missing from the phi bin
// dir and from PATH, i.e. it needs a download.
func shouldBootstrap(proj *project.Project, name string) bool {
	_, err := proj.Global().LookBin(name)
	return err != nil
}
