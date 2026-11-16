package extension

import (
	"context"
	"os"
	"path/filepath"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/internal/debuglog"
	"github.com/pulseaiclub/phi/internal/extension/core"
	"github.com/pulseaiclub/phi/internal/extension/loader"
)

// Load discovers PXB extensions and spawns each subprocess.
func Load(userDir, projectDir string, options ...core.RunnerOption) (*core.Runner, []loader.Warning, error) {
	found, warns, err := loader.Discover(userDir, projectDir)
	if err != nil {
		return nil, warns, err
	}

	logDir := extensionLogDir(userDir)
	cwd, _ := os.Getwd()

	defaults := []core.RunnerOption{core.WithHost(ext.HostOpts{Cwd: cwd})}
	r := core.NewRunner(logDir, append(defaults, options...)...)

	if len(found) == 0 {
		return r, warns, nil
	}

	ctx := context.Background()
	for _, d := range found {
		if err := r.LoadExtension(ctx, d); err != nil {
			warns = append(warns, loader.Warning{Path: d.Path, Message: err.Error()})
			debuglog.Logf("extension: load %s: %v", d.Path, err)
			continue
		}
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
