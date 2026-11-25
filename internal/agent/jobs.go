package agent

import (
	"errors"

	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
)

// NewJobManager creates a process-level job manager whose runner drives child Engines.
func NewJobManager(
	root string,
	models map[job.Role]llm.ModelConfig,
) (*job.Manager, error) {
	if root == "" {
		return nil, errors.New("agent: jobs root is required")
	}
	return job.New(root, EngineRunner{}, job.WithRoleModels(models))
}
