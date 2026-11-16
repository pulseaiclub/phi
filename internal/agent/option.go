package agent

import (
	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/job"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

// engineConfig holds construction settings. Runtime updates remain on EngineConfigurator.
type engineConfig struct {
	gate               permission.Gate
	ask                permission.AskFunc
	tools              []tooldef.Tool
	jobs               *job.Manager
	extensions         *extension.Runner
	omitExtensionTools bool
	mcp                *mcp.Pool
	hooks              llmclient.Hooks
	configurator       *EngineConfigurator
}

// EngineOption configures an engine before construction.
type EngineOption func(*engineConfig)

// WithGate sets the permission gate (nil = allow all).
func WithGate(gate permission.Gate) EngineOption {
	return func(c *engineConfig) { c.gate = gate }
}

// WithAsk sets the ask callback (nil = deny on Ask).
func WithAsk(ask permission.AskFunc) EngineOption {
	return func(c *engineConfig) { c.ask = ask }
}

// WithTools sets the base tools (nil = default tools; empty = no base tools).
func WithTools(list []tooldef.Tool) EngineOption {
	return func(c *engineConfig) { c.tools = list }
}

// WithJobs registers agent_* tools against the given job manager.
func WithJobs(jobs *job.Manager) EngineOption {
	return func(c *engineConfig) { c.jobs = jobs }
}

// WithExtensions attaches an extension runner (nil = no extensions).
func WithExtensions(runner *extension.Runner) EngineOption {
	return func(c *engineConfig) { c.extensions = runner }
}

// WithOmitExtensionTools keeps runner events but skips RegisterTool merge for children.
func WithOmitExtensionTools(omit bool) EngineOption {
	return func(c *engineConfig) { c.omitExtensionTools = omit }
}

// WithMCP attaches the pool used by the system prompt and mcp:// tool URLs.
func WithMCP(pool *mcp.Pool) EngineOption {
	return func(c *engineConfig) { c.mcp = pool }
}

// WithHooks sets provider request interceptors (nil fields = no customization).
func WithHooks(hooks llmclient.Hooks) EngineOption {
	return func(c *engineConfig) { c.hooks = hooks }
}

// WithConfigurator attaches the runtime configuration handle (nil = unmanaged).
// The host claims it via Register and publishes a complete spec through Set.
func WithConfigurator(configurator *EngineConfigurator) EngineOption {
	return func(c *engineConfig) { c.configurator = configurator }
}
