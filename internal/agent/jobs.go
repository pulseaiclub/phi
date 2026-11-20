package agent

import (
	"errors"

	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
)

// NewJobManager creates a process-level job manager whose runner drives child Engines.
// extensionsFn supplies extensions for child engines (may return nil); prefer a live
// getter so TUI reload updates sub-agents too.
func NewJobManager(
	root string,
	models map[job.Role]llm.ModelConfig,
	extensionsFn func() *extension.Runner,
) (*job.Manager, error) {
	if root == "" {
		return nil, errors.New("agent: jobs root is required")
	}
	return job.New(job.Options{
		Root:       root,
		RoleModels: models,
		Runner: EngineRunner{
			ExtensionsFn: extensionsFn,
		},
	})
}
