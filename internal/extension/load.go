package extension

import (
	"context"
	"os"
	"path/filepath"

	"github.com/pulseaiclub/phi/internal/debuglog"
	"github.com/pulseaiclub/phi/internal/extension/core"
	"github.com/pulseaiclub/phi/internal/extension/loader"
)

// Load discovers PXB extensions and spawns each subprocess.
func Load(userDir, projectDir string) (*core.Runner, []loader.Warning, error) {
	return LoadWithHost(userDir, projectDir, HostConfig{})
}

// LoadWithHost discovers PXB extensions and starts them with the host state
// already installed. Initial topology changes are intentionally silent.
func LoadWithHost(userDir, projectDir string, cfg HostConfig) (*core.Runner, []loader.Warning, error) {
	found, warns, err := loader.Discover(userDir, projectDir)
	if err != nil {
		return nil, warns, err
	}

	logDir := extensionLogDir(userDir)
	cwd, _ := os.Getwd()
	if cfg.Host.Cwd == "" {
		cfg.Host.Cwd = cwd
	}

	r := core.NewRunnerWithHost(logDir, cfg)

	if len(found) == 0 {
		return r, warns, nil
	}

	// Naming is already de-duplicated by the loader, so a plain insert is safe.
	env := core.StartEnv{LogDir: logDir, Cwd: cfg.Host.Cwd, SessionID: cfg.Host.SessionID}
	ctx := context.Background()
	for _, d := range found {
		f := core.NewFiber(d.Manifest.Name, d)
		if err := f.Load(ctx, env); err != nil {
			warns = append(warns, loader.Warning{Path: d.Path, Message: err.Error()})
			debuglog.Logf("extension: load %s: %v", d.Path, err)
			continue
		}
		r.AddFiberInitial(f)
	}
	return r, warns, nil
}

func extensionLogDir(userDir string) string {
	if userDir == "" {
		return filepath.Join(os.TempDir(), "phi-ext-logs")
	}
	// ~/.phi/extensions → ~/.phi/logs
	return filepath.Join(filepath.Dir(userDir), "logs")
}
