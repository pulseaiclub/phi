package agent

import (
	"errors"

	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

// JobOptions groups everything needed to spawn a child agent job.
type JobOptions struct {
	Model   llm.ModelConfig
	ModelFn func(role job.Role) llm.ModelConfig // if set, preferred over Model
	Tools   []tooldef.Tool                      // nil → SpecForRole(job.Role).Tools
}

// NewJobManager creates a process-level job manager whose runner drives child Engines.
// modelFn may be nil; then model is used as a fixed snapshot for every role.
func NewJobManager(
	root string,
	model llm.ModelConfig,
	modelFn func(role job.Role) llm.ModelConfig,
) (*job.Manager, error) {
	if root == "" {
		return nil, errors.New("agent: jobs root is required")
	}
	return job.New(job.Options{
		Root: root,
		Runner: &EngineRunner{
			Model:   model,
			ModelFn: modelFn,
		},
	})
}
