package agent

import (
	"errors"

	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
)

// NewJobManager creates a process-level job manager whose runner drives child Engines.
// parent is the model configuration used when modelFn is nil.
// When set, modelFn receives the job role so callers can pick per-role models.
func NewJobManager(
	root string,
	parent llm.ModelConfig,
	modelFn func(role job.Role) llm.ModelConfig,
) (*job.Manager, error) {
	if root == "" {
		return nil, errors.New("agent: jobs root is required")
	}
	return job.New(job.Options{
		Root: root,
		Runner: EngineRunner{
			Model:   parent,
			ModelFn: modelFn,
		},
	})
}
