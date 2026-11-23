package agent

import (
	"errors"

	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
)

// NewJobManager creates a process-level job manager whose runner drives child Engines.
// extensionsFn supplies extensions for child engines (may return nil); prefer a live
// getter so TUI reload updates sub-agents too.
func NewJobManager(
	root string,
	models map[job.Role]llm.ModelConfig,
	extensionsFn func() Extensions,
) (*job.Manager, error) {
	if root == "" {
		return nil, errors.New("agent: jobs root is required")
	}
	return job.New(root, EngineRunner{
		ExtensionsFn: extensionsFn,
	}, job.WithRoleModels(models))
}
