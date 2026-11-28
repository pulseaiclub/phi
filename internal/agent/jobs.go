package agent

import (
	"errors"

	"github.com/pulseaiclub/phi/internal/core"
	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/job"
)

// NewJobManager creates a process-level job manager whose runner drives child Engines.
// extensionsFn supplies extensions for child engines (may return nil); prefer a live
// getter so TUI reload updates sub-agents too.
func NewJobManager(
	root string,
	models map[job.Role]core.ModelConfig,
	extensionsFn func() extension.Extensions,
) (*job.Manager, error) {
	if root == "" {
		return nil, errors.New("agent: jobs root is required")
	}
	return job.New(root, EngineRunner{
		ExtensionsFn: extensionsFn,
	}, job.WithRoleModels(models))
}
