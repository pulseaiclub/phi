package agent

import (
	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/job"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

// EngineConfigOption configures optional NewEngine dependencies.
type EngineConfigOption interface {
	Apply(config EngineConfig) EngineConfig
}

type EngineConfigOptionFunc func(config EngineConfig) EngineConfig

func (f EngineConfigOptionFunc) Apply(config EngineConfig) EngineConfig {
	return f(config)
}

type EngineConfig struct {
	tools        []tooldef.Tool
	jobs         *job.Manager
	extensions   *extension.Runner
	omitExtTools bool
	mcp          *mcp.Pool
	hooks        llmclient.Hooks
}

// WithTools sets the base tool list (nil = tools.DefaultTools(mcp) when an MCP
// pool is attached; sub-agents use ChildTools()).
func WithTools(list []tooldef.Tool) EngineConfigOption {
	return EngineConfigOptionFunc(func(c EngineConfig) EngineConfig {
		c.tools = list
		return c
	})
}

// WithJobs registers agent_* tools against the given job manager.
func WithJobs(jobs *job.Manager) EngineConfigOption {
	return EngineConfigOptionFunc(func(c EngineConfig) EngineConfig {
		c.jobs = jobs
		return c
	})
}

// WithExtensions attaches an extension runner (nil = no extensions).
func WithExtensions(runner *extension.Runner) EngineConfigOption {
	return EngineConfigOptionFunc(func(c EngineConfig) EngineConfig {
		c.extensions = runner
		return c
	})
}

// WithOmitExtensionTools keeps Runner events but skips RegisterTool merge (sub-agents).
// The child also leaves the shared runner's host metadata and base tools with the parent.
func WithOmitExtensionTools(omit bool) EngineConfigOption {
	return EngineConfigOptionFunc(func(c EngineConfig) EngineConfig {
		c.omitExtTools = omit
		return c
	})
}

// WithMCP attaches the MCP pool: server names enter the system prompt and the
// executor resolves mcp:// URLs in ls/read/write against it.
func WithMCP(pool *mcp.Pool) EngineConfigOption {
	return EngineConfigOptionFunc(func(c EngineConfig) EngineConfig {
		c.mcp = pool
		return c
	})
}

// WithHooks sets provider request interceptors (nil fields = no customization).
func WithHooks(hooks llmclient.Hooks) EngineConfigOption {
	return EngineConfigOptionFunc(func(c EngineConfig) EngineConfig {
		c.hooks = hooks
		return c
	})
}
