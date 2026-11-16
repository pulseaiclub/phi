package agent

import (
	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/job"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

// EngineConfig contains the complete runtime configuration for an Engine.
// A configurator publishes a whole value so readers never observe a partial update.
type EngineConfig struct {
	Gate               permission.Gate
	Ask                permission.AskFunc
	Tools              []tooldef.Tool
	Jobs               *job.Manager
	Extensions         *extension.Runner
	OmitExtensionTools bool
	MCP                *mcp.Pool
	Hooks              llmclient.Hooks
}

// EngineOption configures optional NewEngine dependencies.
type EngineOption func(*EngineConfig)

// WithGate sets the permission gate (nil = allow all).
func WithGate(gate permission.Gate) EngineOption {
	return func(c *EngineConfig) { c.Gate = gate }
}

// WithAsk sets the ask callback (nil = deny on Ask).
func WithAsk(ask permission.AskFunc) EngineOption {
	return func(c *EngineConfig) { c.Ask = ask }
}

// WithTools sets the base tool list (nil = tools.DefaultTools(mcp) when an MCP
// pool is attached; sub-agents use ChildTools()).
func WithTools(list []tooldef.Tool) EngineOption {
	return func(c *EngineConfig) { c.Tools = list }
}

// WithJobs registers agent_* tools against the given job manager.
func WithJobs(jobs *job.Manager) EngineOption {
	return func(c *EngineConfig) { c.Jobs = jobs }
}

// WithExtensions attaches an extension runner (nil = no extensions).
func WithExtensions(runner *extension.Runner) EngineOption {
	return func(c *EngineConfig) { c.Extensions = runner }
}

// WithOmitExtensionTools keeps Runner events but skips RegisterTool merge (sub-agents).
// The child also leaves the shared runner's host metadata and base tools with the parent.
func WithOmitExtensionTools(omit bool) EngineOption {
	return func(c *EngineConfig) { c.OmitExtensionTools = omit }
}

// WithMCP attaches the MCP pool: server names enter the system prompt and the
// executor resolves mcp:// URLs in ls/read/write against it.
func WithMCP(pool *mcp.Pool) EngineOption {
	return func(c *EngineConfig) { c.MCP = pool }
}

// WithHooks sets provider request interceptors (nil fields = no customization).
func WithHooks(hooks llmclient.Hooks) EngineOption {
	return func(c *EngineConfig) { c.Hooks = hooks }
}
