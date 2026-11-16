package agent

import (
	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/job"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

// EngineConfig holds the static dependencies NewEngine injects once at
// construction. The hot-reconfigurable part (model, session, extension
// runner) lives in EngineSpec, published through EngineConfigurator.
type EngineConfig struct {
	Gate               permission.Gate
	Ask                permission.AskFunc
	Tools              []tooldef.Tool
	Jobs               *job.Manager
	Extensions         *extension.Runner
	OmitExtensionTools bool
	MCP                *mcp.Pool
	Hooks              llmclient.Hooks
	Configurator       *EngineConfigurator
}

type EngineOption interface {
	Apply(EngineConfig) EngineConfig
}

// EngineOptionFunc configures optional NewEngine dependencies.
type EngineOptionFunc func(EngineConfig) EngineConfig

func (f EngineOptionFunc) Apply(config EngineConfig) EngineConfig {
	return f(config)
}

// WithGate sets the permission gate (nil = allow all).
func WithGate(gate permission.Gate) EngineOption {
	return EngineOptionFunc(func(c EngineConfig) EngineConfig {
		c.Gate = gate
		return c
	})
}

// WithAsk sets the ask callback (nil = deny on Ask).
func WithAsk(ask permission.AskFunc) EngineOption {
	return EngineOptionFunc(func(c EngineConfig) EngineConfig {
		c.Ask = ask
		return c
	})
}

// WithTools sets the base tool list (nil = tools.DefaultTools(mcp) when an MCP
// pool is attached; sub-agents use ChildTools()).
func WithTools(list []tooldef.Tool) EngineOption {
	return EngineOptionFunc(func(c EngineConfig) EngineConfig {
		c.Tools = list
		return c
	})
}

// WithJobs registers agent_* tools against the given job manager.
func WithJobs(jobs *job.Manager) EngineOption {
	return EngineOptionFunc(func(c EngineConfig) EngineConfig {
		c.Jobs = jobs
		return c
	})
}

// WithExtensions attaches an extension runner (nil = no extensions).
func WithExtensions(runner *extension.Runner) EngineOption {
	return EngineOptionFunc(func(c EngineConfig) EngineConfig {
		c.Extensions = runner
		return c
	})
}

// WithOmitExtensionTools keeps Runner events but skips RegisterTool merge (sub-agents).
// The child also leaves the shared runner's host metadata and base tools with the parent.
func WithOmitExtensionTools(omit bool) EngineOption {
	return EngineOptionFunc(func(c EngineConfig) EngineConfig {
		c.OmitExtensionTools = omit
		return c
	})
}

// WithMCP attaches the MCP pool: server names enter the system prompt and the
// executor resolves mcp:// URLs in ls/read/write against it.
func WithMCP(pool *mcp.Pool) EngineOption {
	return EngineOptionFunc(func(c EngineConfig) EngineConfig {
		c.MCP = pool
		return c
	})
}

// WithHooks sets provider request interceptors (nil fields = no customization).
func WithHooks(hooks llmclient.Hooks) EngineOption {
	return EngineOptionFunc(func(c EngineConfig) EngineConfig {
		c.Hooks = hooks
		return c
	})
}

// WithConfigurator attaches the handle that manages this engine's EngineSpec
// (nil = unmanaged: sub-agents, standalone runs). The host claims the handle
// via Register and republishes a complete spec on every change; each Set
// rebuilds the engine, so the handle outlives any single Engine.
func WithConfigurator(configurator *EngineConfigurator) EngineOption {
	return EngineOptionFunc(func(c EngineConfig) EngineConfig {
		c.Configurator = configurator
		return c
	})
}
