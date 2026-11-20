package agent

import (
	"context"
	"errors"
	"iter"

	"github.com/pulseaiclub/phi/ext/go/pxb"
	"github.com/pulseaiclub/phi/internal/agent/prompt"
	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
	"github.com/pulseaiclub/phi/internal/llm/extmodel"
	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/tools"
	"github.com/pulseaiclub/phi/internal/tools/agenttool"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

// EngineConfig supplies the collaborators used to assemble an Engine.
type EngineConfig struct {
	Tools              []tooldef.Tool
	Jobs               *job.Manager
	Extensions         *extension.Runner
	OmitExtensionTools bool
	MCP                *mcp.Pool
	Hooks              llmclient.Hooks
}

// CoreTools returns the built-in and agent_* tools for sess. It does not modify
// the session, job manager, MCP pool, or extension runner.
func (cfg EngineConfig) CoreTools(sess *Session) []tooldef.Tool {
	base := cfg.Tools
	if base == nil {
		base = tools.DefaultTools(cfg.MCP)
	}
	if cfg.Jobs == nil {
		return base
	}
	agentTools := agenttool.AgentTools(
		agenttool.WithManager(cfg.Jobs),
		agenttool.WithParentID(sess.ID),
		agenttool.WithWorkDir(sess.Cwd),
	)
	out := make([]tooldef.Tool, 0, len(base)+len(agentTools))
	out = append(out, base...)
	out = append(out, agentTools...)
	return out
}

// NewEngine assembles the client, executor, prompt, and tool list for a loop.
func (cfg EngineConfig) NewEngine(model llm.ModelConfig, sess *Session) (*Engine, error) {
	if sess == nil {
		return nil, errors.New("agent: session is required")
	}
	coreTools := cfg.CoreTools(sess)
	toolList := coreTools
	if !cfg.OmitExtensionTools {
		if extTools := cfg.Extensions.ExtensionTools(); len(extTools) > 0 {
			toolList = make([]tooldef.Tool, 0, len(coreTools)+len(extTools))
			toolList = append(toolList, coreTools...)
			toolList = append(toolList, extTools...)
		}
	}

	var mcpServers []string
	if cfg.MCP != nil {
		mcpServers = cfg.MCP.ServerNames()
	}
	maxConcurrent := 0
	if cfg.Jobs != nil {
		maxConcurrent = cfg.Jobs.MaxConcurrent()
	}
	system := prompt.Build(sess.Cwd(), model.SkillPaths, cfg.Jobs != nil, maxConcurrent, mcpServers)
	defs := tooldef.Definitions(toolList)
	var client llm.ChatClient
	if route, ok := cfg.Extensions.LookupModel(model.Name); ok && route.ExtConn() {
		runner := cfg.Extensions
		stream := func(ctx context.Context, req pxb.ModelInvoke) (iter.Seq2[pxb.ModelStreamEvent, error], error) {
			return runner.ModelStream(ctx, route, req)
		}
		client = extmodel.New(route.Reg.Name, stream, model, defs, system)
	} else {
		client = llmclient.NewClient(model, cfg.Hooks, defs, system)
	}
	executor := NewExecutor(tooldef.NewRegistry(toolList), sess.Cwd(), cfg.Extensions)
	return NewEngine(model, sess, client, executor)
}
