package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"time"

	"github.com/pulseaiclub/phi/internal/agent/prompt"
	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
	"github.com/pulseaiclub/phi/internal/llm/skills"
	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/session/compaction"
	"github.com/pulseaiclub/phi/internal/tools"
)

// ErrMaxRounds is returned (wrapped) by Loop when the model exceeds the
// configured tool-round budget and continuation is declined or unavailable.
// Callers can distinguish it from other runtime errors with errors.Is,
// e.g. for a dedicated exit code.
var ErrMaxRounds = errors.New("exceeded maximum tool rounds")

const defaultMaxToolRounds = 64

// ContinueFunc asks whether to grant another maxRounds budget after the
// current budget is exhausted. Nil means hard-fail with ErrMaxRounds
// (headless / sub-agent default). True continues the loop with a fresh budget.
type ContinueFunc func(ctx context.Context, maxRounds int) (bool, error)

// Engine drives the agent loop: stream → tools → stream…
// and yields session.Event for the TUI reducer. Context compaction is owned
// here so Session stays a thin message store.
type Engine struct {
	client       *llmclient.Client
	executor     *Executor
	maxRounds    int
	modelCfg     llm.ModelConfig
	hooks        llmclient.Hooks
	gate         permission.Gate
	ask          permission.AskFunc
	continueAsk  ContinueFunc
	jobs         *job.Manager
	extensions   *extension.Runner // nil = disabled; all methods are nil-safe no-ops
	baseTools    []tools.Tool      // nil = DefaultTools; preserved across rebind
	omitExtTools bool              // sub-agents: emit events but skip RegisterTool merge
	mcp          *mcp.Pool
	authFailure  AuthFailureFunc // terminal-401 notification; nil = ignore

	session *Session
}

// NewEngine wires an LLM client, tool executor, and an externally created session.
func NewEngine(model llm.ModelConfig, sess *Session, opts ...EngineOption) (*Engine, error) {
	if sess == nil {
		return nil, errors.New("agent: session is required")
	}
	var cfg engineConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	engine := &Engine{
		maxRounds:    defaultMaxToolRounds,
		modelCfg:     model,
		hooks:        cfg.hooks,
		session:      sess,
		gate:         cfg.gate,
		ask:          cfg.ask,
		continueAsk:  cfg.continueAsk,
		jobs:         cfg.jobs,
		extensions:   cfg.extensions,
		omitExtTools: cfg.omitExtTools,
		mcp:          cfg.mcp,
		authFailure:  cfg.authFailure,
	}
	if cfg.maxRounds > 0 {
		engine.maxRounds = cfg.maxRounds
	}
	engine.baseTools = cfg.tools
	engine.extensions.SetBaseTools(engine.buildCoreTools(engine.baseTools))
	engine.extensions.SetMeta(engine.SessionID(), engine.SessionCwd())
	toolList := engine.buildToolList(engine.baseTools)
	engine.client = llmclient.NewClient(model, engine.hooks, tools.Definitions(toolList), engine.systemPrompt())
	engine.bindExecutor(tools.NewRegistry(toolList))
	return engine, nil
}

func (engine *Engine) buildToolList(base []tools.Tool) []tools.Tool {
	out := engine.buildCoreTools(base)
	if !engine.omitExtTools {
		if extTools := engine.extensions.ExtensionTools(); len(extTools) > 0 {
			merged := make([]tools.Tool, 0, len(out)+len(extTools))
			merged = append(merged, out...)
			merged = append(merged, extTools...)
			return merged
		}
	}
	return out
}

// buildCoreTools returns builtin (+ MCP + agent_*) tools without extension RegisterTool.
func (engine *Engine) buildCoreTools(base []tools.Tool) []tools.Tool {
	if base == nil {
		base = tools.DefaultTools()
	}
	out := base
	if engine.mcp != nil {
		mcpTools := tools.MCPTools(engine.mcp)
		if len(mcpTools) > 0 {
			merged := make([]tools.Tool, 0, len(out)+len(mcpTools))
			merged = append(merged, out...)
			merged = append(merged, mcpTools...)
			out = merged
		}
	}
	if engine.jobs == nil {
		return out
	}
	agentTools := tools.AgentTools(tools.AgentDeps{
		Manager:  engine.jobs,
		ParentID: engine.SessionID,
		WorkDir:  engine.SessionCwd,
	})
	merged := make([]tools.Tool, 0, len(out)+len(agentTools))
	merged = append(merged, out...)
	merged = append(merged, agentTools...)
	return merged
}

// SetModel replaces the LLM client and model-related settings without
// discarding the session tree. Agent tools remain registered when Jobs is set.
// Hooks are left unchanged; use SetModelWithHooks when switching presets.
func (engine *Engine) SetModel(cfg llm.ModelConfig) {
	engine.SetModelWithHooks(cfg, engine.hooks)
}

// SetModelWithHooks replaces the model config and request hooks together.
func (engine *Engine) SetModelWithHooks(cfg llm.ModelConfig, hooks llmclient.Hooks) {
	engine.modelCfg = cfg
	engine.hooks = hooks
	engine.rebindTools()
}

// SetJobs attaches or detaches the job manager and rebuilds the tool list.
// Pass nil to unregister agent_* tools (sub-agents disabled).
func (engine *Engine) SetJobs(jobs *job.Manager) {
	if engine == nil {
		return
	}
	engine.jobs = jobs
	engine.rebindTools()
}

func (engine *Engine) rebindTools() {
	engine.extensions.SetBaseTools(engine.buildCoreTools(engine.baseTools))
	toolList := engine.buildToolList(engine.baseTools)
	engine.client = llmclient.NewClient(
		engine.modelCfg,
		engine.hooks,
		tools.Definitions(toolList),
		engine.systemPrompt(),
	)
	engine.bindExecutor(tools.NewRegistry(toolList))
}

func (engine *Engine) systemPrompt() string {
	var mcpServers []string
	if engine.mcp != nil {
		mcpServers = engine.mcp.ServerNames()
	}
	maxConcurrent := 0
	if engine.jobs != nil {
		maxConcurrent = engine.jobs.MaxConcurrent()
	}
	return prompt.Build(engine.modelCfg.SkillPath, engine.jobs != nil, maxConcurrent, mcpServers)
}

func (engine *Engine) bindExecutor(registry tools.Registry) {
	engine.executor = NewExecutor(registry, engine.gate, engine.ask, engine.extensions)
	engine.executor.SetMeta(engine.SessionID(), engine.SessionCwd())
}

// HasTool reports whether a tool is currently registered on the executor.
func (engine *Engine) HasTool(name string) bool {
	if engine == nil || engine.executor == nil {
		return false
	}
	_, ok := engine.executor.registry[name]
	return ok
}

// SetPermission updates the gate and ask handler used by the tool executor.
func (engine *Engine) SetPermission(gate permission.Gate, ask permission.AskFunc) {
	if engine == nil {
		return
	}
	engine.gate = gate
	engine.ask = ask
	if engine.executor != nil {
		engine.executor.gate = gate
		engine.executor.ask = ask
	}
}

// SetContinueAsk sets the handler invoked when the tool-round budget is exhausted.
// Pass nil to hard-fail with ErrMaxRounds (default for headless runs).
func (engine *Engine) SetContinueAsk(fn ContinueFunc) {
	if engine == nil {
		return
	}
	engine.continueAsk = fn
}

// SetExtensions replaces the extension runner. Pass nil to disable extensions.
// Rebinds tools so RegisterTool from the new runner takes effect.
func (engine *Engine) SetExtensions(r *extension.Runner) {
	if engine == nil {
		return
	}
	engine.extensions = r
	engine.rebindTools()
}

// SessionID returns the durable session id.
func (engine *Engine) SessionID() string {
	if engine == nil || engine.session == nil {
		return ""
	}
	return engine.session.ID()
}

// SessionFile returns the JSONL path (empty in memory mode).
func (engine *Engine) SessionFile() string {
	if engine == nil || engine.session == nil {
		return ""
	}
	return engine.session.File()
}

// SessionCwd returns the cwd recorded on the session header.
func (engine *Engine) SessionCwd() string {
	if engine == nil || engine.session == nil {
		return ""
	}
	return engine.session.Cwd()
}

// Session returns the underlying session wrapper (for UI transcript replay).
func (engine *Engine) Session() *Session {
	if engine == nil {
		return nil
	}
	return engine.session
}

// LoopOpts configures a single agent loop turn.
type LoopOpts struct {
	// PendingSkills are skill names the user selected in the composer.
	// When set, the model is instructed to read those SKILL.md files first.
	PendingSkills []string
	// Images are base64 attachments from the composer pending queue.
	Images []llm.Image
}

// Loop appends the user prompt and runs inference + tool rounds until the
// model stops calling tools or the context is cancelled.
//
// Compaction runs in two places:
//  1. After a finished turn (final assistant with no tool_calls), when usage
//     crosses the context-window threshold.
//  2. Mid-loop on a context-overflow API error: force-compact once, rebuild
//     context, and retry the stream. A second overflow fails closed.
func (engine *Engine) Loop(ctx context.Context, prompt string, opts LoopOpts) iter.Seq2[session.Event, error] {
	return func(yield func(session.Event, error) bool) {
		// The extension runner is nil-safe (nil = disabled), so calls are
		// unconditional no-ops when no extensions are loaded.
		content, handled := engine.extensions.EmitUserInput(prompt)
		if handled {
			return
		}
		if instr := pendingSkillsInstruction(engine.modelCfg.SkillPath, opts.PendingSkills); instr != "" {
			if content == "" {
				content = instr
			} else {
				content = instr + "\n\n" + content
			}
		}
		rewritten, extra := engine.extensions.EmitBeforeAgentStart(content)
		content = rewritten
		if extra != "" {
			content = content + "\n\n" + extra
		}
		engine.extensions.EmitAgentStart()
		defer engine.extensions.EmitAgentEnd()
		if err := engine.session.Append(llm.Message{
			Role:    llm.RoleUser,
			Content: content,
			Images:  append([]llm.Image(nil), opts.Images...),
		}); err != nil {
			yield(nil, err)
			return
		}

		toolRounds := 0
		overflowRecovered := false
		for {
			if ctx.Err() != nil {
				return
			}

			engine.extensions.EmitTurnStart(toolRounds)

			msgs := engine.session.BuildContext()

			msg, completeEvent, err := engine.streamTurn(ctx, yield, msgs)
			if err != nil {
				if !overflowRecovered && llm.IsContextOverflow(err) {
					did, cerr := engine.runCompact(ctx, yield, 0, true)
					if cerr == nil && did {
						overflowRecovered = true
						continue
					}
				}
				yield(nil, err)
				return
			}
			if completeEvent == nil {
				// Cancelled or consumer stopped — no error to surface.
				return
			}

			// Defer publishing and persisting the terminal assistant update until
			// the tool budget is checked. An over-budget tool request must not
			// leave an unexecuted tool call in the session or UI.
			if len(msg.ToolCalls) > 0 && toolRounds >= engine.maxRounds {
				if engine.continueAsk == nil {
					yield(nil, fmt.Errorf("agent: %w (%d)", ErrMaxRounds, engine.maxRounds))
					return
				}
				ok, askErr := engine.continueAsk(ctx, engine.maxRounds)
				if askErr != nil {
					yield(nil, askErr)
					return
				}
				if !ok {
					yield(nil, fmt.Errorf("agent: %w (%d)", ErrMaxRounds, engine.maxRounds))
					return
				}
				// Granted: reset the budget; the current and following tool
				// rounds run under the fresh budget.
				toolRounds = 0
			}
			if !yield(completeEvent, nil) {
				return
			}

			if err := engine.session.Append(msg); err != nil {
				yield(nil, err)
				return
			}

			if len(msg.ToolCalls) == 0 {
				engine.extensions.EmitTurnEnd(toolRounds)
				if cont, steer := engine.extensions.EmitTurnStopping(toolRounds); cont {
					steerMsg := steer
					if steerMsg == "" {
						steerMsg = "continue"
					}
					if err := engine.session.Append(
						llm.Message{Role: llm.RoleUser, Content: steerMsg},
					); err != nil {
						yield(nil, err)
						return
					}
					continue
				}
				// Turn finished — compact using this assistant's usage. The
				// threshold reads context size the same way the composer does.
				if _, err := engine.runCompact(ctx, yield, msg.Usage.ContextTokens(), false); err != nil {
					yield(nil, err)
				}
				return
			}

			toolRounds++
			toolMsgs, stop, _ := engine.executor.Run(ctx, msg.ToolCalls, func(td session.ToolData) bool {
				return yield(td, nil)
			})
			if err := engine.session.Append(toolMsgs...); err != nil {
				yield(nil, err)
				return
			}
			engine.extensions.EmitTurnEnd(toolRounds - 1)
			if stop {
				return
			}

			if ctx.Err() != nil {
				return
			}
		}
	}
}

// runCompact prepares and persists a compaction entry.
// When force is false, it no-ops unless usage crosses the window threshold.
// When force is true (overflow recovery), it skips the threshold check but
// still no-ops when there is nothing useful to summarize (avoids burning the
// single overflow retry on a no-op compact).
// did is true only when a compaction entry was appended.
func (engine *Engine) runCompact(
	ctx context.Context,
	yield func(session.Event, error) bool,
	usage int,
	force bool,
) (did bool, err error) {
	settings := compaction.DefaultSettings()
	if engine.client == nil {
		return false, nil
	}
	if !force && !compaction.ShouldCompact(usage, engine.modelCfg.ContextWindow, settings) {
		return false, nil
	}
	prep, err := compaction.PrepareCompact(engine.session.PathEntries(), settings)
	if err != nil {
		return false, err
	}
	if prep.FirstKeptEntryId == "" {
		return false, nil
	}
	if force && len(prep.MessagesToSummarize) == 0 && len(prep.TurnPrefixMessages) == 0 {
		return false, nil
	}

	id := fmt.Sprintf("compaction-%d", time.Now().UnixNano())
	if !yield(session.CompactionStarted{}, nil) {
		return false, context.Canceled
	}

	comp, err := compaction.Compact(ctx, *prep, engine.client)
	if err != nil {
		engine.notifyAuthFailure(err)
		_ = yield(session.CompactionComplete{ID: id, Failed: true}, nil)
		return false, err
	}
	if err := engine.session.AppendCompaction(comp); err != nil {
		_ = yield(session.CompactionComplete{ID: id, Failed: true}, nil)
		return false, err
	}
	if !yield(session.CompactionComplete{ID: id, TokensBefore: comp.TokensBefore}, nil) {
		return false, context.Canceled
	}
	engine.extensions.EmitSessionCompact("auto")
	return true, nil
}

// notifyAuthFailure reports a terminal 401 to the callback registered with
// WithAuthFailure. A durable credential cannot be refreshed, so the callback
// marks the exact account and generation that was rejected; the error is still
// returned unchanged so the user sees why the turn stopped.
func (engine *Engine) notifyAuthFailure(err error) {
	if engine == nil || engine.authFailure == nil || err == nil {
		return
	}
	if llm.IsUnauthorized(err) {
		engine.authFailure(engine.modelCfg)
	}
}

// streamTurn runs one assistant stream. On success it returns the final
// message and a StateComplete event (not yet yielded — caller checks tool
// budget first). On API/stream failure it returns err without yielding it
// so Loop can attempt overflow recovery. A nil event with nil err means
// cancel or the consumer stopped receiving.
func (engine *Engine) streamTurn(
	ctx context.Context,
	yield func(session.Event, error) bool,
	messages []llm.Message,
) (llm.Message, session.Event, error) {
	id := fmt.Sprintf("assistant-%d", time.Now().UnixNano())
	var thinking, text string
	var final llm.Message
	gotDone := false

	for event, err := range engine.client.Stream(ctx, messages) {
		if err != nil {
			engine.notifyAuthFailure(err)
			if thinking != "" || text != "" {
				_ = yield(emitMessage(id, session.StateError, session.StopNone, thinking, text, nil, llm.Usage{}), nil)
			}
			return llm.Message{}, nil, err
		}

		switch event.Type {
		case llm.StreamEventTypeError:
			errText := event.Err
			if errText == "" {
				errText = "stream error"
			}
			return llm.Message{}, nil, fmt.Errorf("%s", errText)

		case llm.StreamEventTypeDelta:
			if event.Delta.ReasoningContent != "" {
				thinking += event.Delta.ReasoningContent
			}
			if event.Delta.Content != "" {
				text += event.Delta.Content
			}
			if !yield(
				emitMessage(id, session.StateStreaming, session.StopNone, thinking, text, nil, llm.Usage{}),
				nil,
			) {
				return llm.Message{}, nil, nil
			}

		case llm.StreamEventTypeDone:
			if event.Final == nil {
				return llm.Message{}, nil, errors.New("agent: stream finished with no assistant message")
			}
			final = *event.Final
			gotDone = true
			// Prefer fully accumulated message for the complete event.
			if final.ReasoningContent != "" {
				thinking = final.ReasoningContent
			}
			if final.Content != "" {
				text = final.Content
			}
		}
	}

	if !gotDone {
		if ctx.Err() != nil {
			_ = yield(emitMessage(id, session.StateCancelled, session.StopNone, thinking, text, nil, llm.Usage{}), nil)
			return llm.Message{}, nil, nil
		}
		return llm.Message{}, nil, errors.New("agent: stream closed without assistant output")
	}

	reason := session.StopEndTurn
	if len(final.ToolCalls) > 0 {
		reason = session.StopToolUse
	}
	complete := session.AssistantMessageUpdate{Message: session.ProjectAssistant(
		id,
		final,
		func(name, args string) string { return engine.ToolDetail(name, args) },
		session.StateComplete,
		reason,
		session.TokenUsageFrom(final.Usage),
	)}
	return final, complete, nil
}

// ToolDetail resolves a friendly one-line detail for a tool call's raw JSON
// arguments via the tool's DetailFromArgs, matching the live executor. It is
// used both when building live tool_use blocks and by UI transcript replay so
// resumed sessions show the same detail (e.g. "read foo.go:10-20") instead of
// raw JSON. Returns "" when the tool is unknown or has no detail formatter.
func (engine *Engine) ToolDetail(name, args string) string {
	if engine == nil || engine.executor == nil {
		return ""
	}
	tool, ok := engine.executor.registry[name]
	if !ok || tool.DetailFromArgs == nil {
		return ""
	}
	return tool.DetailFromArgs(json.RawMessage(args))
}

func buildContent(thinking, text string, tools []session.ContentBlock) []session.ContentBlock {
	var out []session.ContentBlock
	if thinking != "" {
		out = append(out, session.ContentBlock{Type: session.BlockThinking, Text: thinking})
	}
	if text != "" {
		out = append(out, session.ContentBlock{Type: session.BlockText, Text: text})
	}
	out = append(out, tools...)
	return out
}

func emitMessage(
	id string,
	state session.State,
	reason session.StopReason,
	thinking,
	text string,
	tools []session.ContentBlock,
	usage llm.Usage,
) session.Event {
	return session.AssistantMessageUpdate{Message: session.Message{
		ID:         id,
		State:      state,
		StopReason: reason,
		Content:    buildContent(thinking, text, tools),
		Text:       text,
		Usage:      session.TokenUsageFrom(usage),
	}}
}

// pendingSkillsInstruction tells the model to read SKILL.md files for the
// selected skills (reuse the read tool, no dedicated skill tool).
func pendingSkillsInstruction(skillPath string, names []string) string {
	if len(names) == 0 {
		return ""
	}
	list, err := skills.LoadSkills(skillPath)
	targets := make([]string, 0, len(names))
	if err == nil {
		for _, name := range names {
			if s := skills.Find(list, name); s != nil && s.SkillFilePath != "" {
				targets = append(targets, s.SkillFilePath)
				continue
			}
			targets = append(targets, name)
		}
	} else {
		targets = append(targets, names...)
	}
	return fmt.Sprintf(
		"You MUST read these skill files first with the read tool and follow them: %s. Do this immediately before responding.",
		strings.Join(targets, ", "),
	)
}
