package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/toolmanager"
)

const bootstrapDownloadTimeout = 5 * time.Minute

// phi run exit codes.
const (
	ExitOK        = 0 // loop finished without errors
	ExitError     = 1 // runtime / LLM / session error
	ExitMaxRounds = 2 // model exceeded --max-rounds
	ExitUsage     = 3 // config or CLI usage error
)

// HeadlessGate builds the permission gate for non-interactive entrypoints.
// An empty policy mode defaults to headless-strict so Ask decisions fold to
// Deny (Ask≡Deny); dangerously_allow_all is honored exactly like the TUI.
func HeadlessGate(policy permission.Policy) (permission.Gate, error) {
	if policy.Mode == "" {
		policy.Mode = permission.ModeHeadlessStrict
	}
	if policy.DangerouslyAllowAll {
		return permission.AllowAll{}, nil
	}
	return permission.NewGate(policy, permission.WorkspaceRoot())
}

// runBootstrap is the shared startup state for headless entrypoints:
// Discover → config → search tools → gate → session dir.
type runBootstrap struct {
	Proj       *project.Project
	Config     *project.Config
	Cwd        string
	SessionDir string
	Gate       permission.Gate
}

// loadRunBootstrap wires the shared startup path used by `phi run` (and any
// future headless subcommand). It must stay in sync with the TUI controller's
// initialization; search-tool install failures are non-fatal warnings.
func loadRunBootstrap(ctx context.Context, sessionDirOverride string) (*runBootstrap, error) {
	proj := project.GetDefaultProject()
	if err := proj.LoadConfig(); err != nil {
		return nil, err
	}
	if err := EnsureSearchTools(ctx, proj); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not install search tools:", err)
	}
	gate, err := HeadlessGate(proj.Config().Permissions)
	if err != nil {
		return nil, fmt.Errorf("permissions: %w", err)
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
		Gate:       gate,
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
	var installErrors []error
	for _, tool := range []string{"fd", "rg"} {
		if !shouldBootstrap(proj, tool) {
			continue
		}
		dlCtx, cancel := context.WithTimeout(ctx, bootstrapDownloadTimeout)
		_, err := download(dlCtx, tool)
		cancel()
		if err != nil {
			installErrors = append(installErrors, fmt.Errorf("%s: %w", tool, err))
		}
	}
	return errors.Join(installErrors...)
}

// shouldBootstrap is true when the tool binary is missing from the phi bin
// dir and from PATH, i.e. it needs a download. This mirrors panda's
// fileutil.ShouldBootstrapSearchTool.
func shouldBootstrap(proj *project.Project, name string) bool {
	binName := name
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	if _, err := os.Stat(filepath.Join(proj.Global().BinDir(), binName)); err == nil {
		return false
	}
	if _, err := exec.LookPath(binName); err == nil {
		return false
	}
	return true
}
