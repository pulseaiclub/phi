package agent

import (
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/tools/agenttool"
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
	tools []tooldef.Tool
	jobs  *agenttool.Binding
	mcp   *mcp.Pool
	hooks llmclient.Hooks
}

// WithTools sets the base tool list (nil = tools.DefaultTools(mcp) when an MCP
// pool is attached; sub-agents use SpecForRole(role).Tools).
func WithTools(list []tooldef.Tool) EngineConfigOption {
	return EngineConfigOptionFunc(func(c EngineConfig) EngineConfig {
		c.tools = list
		return c
	})
}

// WithJobs registers agent_* tools against the given job binding.
func WithJobs(binding *agenttool.Binding) EngineConfigOption {
	return EngineConfigOptionFunc(func(c EngineConfig) EngineConfig {
		c.jobs = binding
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
