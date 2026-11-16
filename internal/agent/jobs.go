package agent

import (
	"errors"

	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
)

type jobsConfig struct {
	modelFn      func(job.Role) llm.ModelConfig
	extensionsFn func() *extension.Runner
}

// JobsOption configures child engine construction.
type JobsOption func(*jobsConfig)

// WithJobsModelFunc selects a model per role (nil = the constructor's snapshot).
func WithJobsModelFunc(fn func(job.Role) llm.ModelConfig) JobsOption {
	return func(c *jobsConfig) { c.modelFn = fn }
}

// WithJobsExtensionsFunc supplies the current extension runner to each child.
// Use a live getter so extension reloads also apply to new children.
func WithJobsExtensionsFunc(fn func() *extension.Runner) JobsOption {
	return func(c *jobsConfig) { c.extensionsFn = fn }
}

// NewJobManager creates a process-level job manager whose runner drives child Engines.
func NewJobManager(root string, model llm.ModelConfig, opts ...JobsOption) (*job.Manager, error) {
	if root == "" {
		return nil, errors.New("agent: jobs root is required")
	}
	var cfg jobsConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	return job.New(root, EngineRunner{
		Model:        model,
		ModelFn:      cfg.modelFn,
		ExtensionsFn: cfg.extensionsFn,
	})
}
