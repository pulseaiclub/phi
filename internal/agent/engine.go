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
	"github.com/pulseaiclub/phi/internal/debuglog"
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
	client        *llmclient.Client
	executor      *Executor
	maxRounds     int
	skillPath     string
	contextWindow int
	modelCfg      llm.ModelConfig
	gate          permission.Gate
	ask           permission.AskFunc
	continueAsk   ContinueFunc
	jobs          *job.Manager
	extensions    *extension.Runner // nil = disabled; all methods are nil-safe no-ops
	baseTools     []tools.Tool      // nil = DefaultTools; preserved across rebind
	omitExtTools  bool              // sub-agents: emit events but skip RegisterTool merge
	mcp           *mcp.Pool

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
		maxRounds:     defaultMaxToolRounds,
		skillPath:     model.SkillPath,
		contextWindow: model.ContextWindow,
		modelCfg:      model,
		session:       sess,
		gate:          cfg.gate,
		ask:           cfg.ask,
		continueAsk:   cfg.continueAsk,
		jobs:          cfg.jobs,
		extensions:    cfg.extensions,
		omitExtTools:  cfg.omitExtTools,
		mcp:           cfg.mcp,
	}
	if cfg.maxRounds > 0 {
		engine.maxRounds = cfg.maxRounds
	}
	engine.baseTools = cfg.tools
	engine.extensions.SetBaseTools(engine.buildCoreTools(engine.baseTools))
	engine.extensions.SetMeta(engine.SessionID(), engine.SessionCwd())
	toolList := engine.buildToolList(engine.baseTools)
	engine.client = llmclient.NewClient(model, tools.Definitions(toolList), engine.systemPrompt())
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
func (engine *Engine) SetModel(cfg llm.ModelConfig) error {
	engine.modelCfg = cfg
	engine.skillPath = cfg.SkillPath
	engine.contextWindow = cfg.ContextWindow
	engine.rebindTools()
	return nil
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
	return prompt.Build(engine.skillPath, engine.jobs != nil, maxConcurrent, mcpServers)
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

// Jobs returns the process-level job manager, if any.
func (engine *Engine) Jobs() *job.Manager {
	if engine == nil {
		return nil
	}
	return engine.jobs
}

// SetMaxRounds bounds the number of tool rounds per Loop call.
// Non-positive values are rejected.
func (engine *Engine) SetMaxRounds(n int) error {
	if engine == nil {
		return nil
	}
	if n <= 0 {
		return fmt.Errorf("agent: max rounds must be positive (got %d)", n)
	}
	engine.maxRounds = n
	return nil
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

// Extensions returns the current extension runner, if any.
func (engine *Engine) Extensions() *extension.Runner {
	if engine == nil {
		return nil
	}
	return engine.extensions
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

// ReplaceSession swaps the session store (used by /resume).
func (engine *Engine) ReplaceSession(sess *Session) error {
	if sess == nil {
		return errors.New("agent: session is required")
	}
	engine.session = sess
	if engine.executor != nil {
		engine.executor.SetMeta(sess.ID(), sess.Cwd())
	}
	return nil
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
// Compaction: persist the turn first, then check usage after
// the agent turn ends (final assistant with no tool_calls) — never mid-tool-loop.
func (engine *Engine) Loop(ctx context.Context, prompt string, opts LoopOpts) iter.Seq2[session.Event, error] {
	return func(yield func(session.Event, error) bool) {
		// The extension runner is nil-safe (nil = disabled), so calls are
		// unconditional no-ops when no extensions are loaded.
		content, handled := engine.extensions.EmitUserInput(prompt)
		if handled {
			return
		}
		if instr := pendingSkillsInstruction(engine.skillPath, opts.PendingSkills); instr != "" {
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
		// Titles are user-facing session metadata; only persisted sessions need
		// the asynchronous extra model request.
		if engine.session.File() != "" && engine.session.Title() == "" {
			go engine.ensureSessionTitle(content)
		}

		toolRounds := 0
		for {
			if ctx.Err() != nil {
				return
			}

			engine.extensions.EmitTurnStart(toolRounds)

			msgs := engine.session.BuildContext()

			msg, completeEvent, ok := engine.streamTurn(ctx, yield, msgs)
			if !ok {
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
				ok, err := engine.continueAsk(ctx, engine.maxRounds)
				if err != nil {
					yield(nil, err)
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
				// Turn finished — compact using this assistant's usage.
				if err := engine.maybeCompact(ctx, yield, msg.Usage.TotalTokens); err != nil {
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

// ensureSessionTitle follows OpenCode's first-real-prompt behavior. Title
// generation is best-effort and never delays or fails the main agent loop.
func (engine *Engine) ensureSessionTitle(prompt string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := llmclient.NewClient(engine.modelCfg, nil, "Generate concise conversation titles. Do not call tools.")
	var title strings.Builder
	for event, err := range client.Stream(ctx, []llm.Message{
		{Role: llm.RoleUser, Content: "Generate a short title for this conversation. Return only the title, without quotes or punctuation at the end.\n\n" + prompt},
	}) {
		if err != nil {
			return
		}
		if event.Type == llm.StreamEventTypeDone && len(event.Partial.Choices) > 0 {
			title.WriteString(event.Partial.Choices[0].Message.Content)
		}
	}
	text := strings.TrimSpace(title.String())
	text = strings.TrimSpace(strings.ReplaceAll(text, "</think>", ""))
	if i := strings.LastIndex(text, "\n"); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	if text == "" {
		return
	}
	if len([]rune(text)) > 100 {
		text = string([]rune(text)[:97]) + "..."
	}
	if engine.session.Title() != "" {
		return
	}
	_ = engine.session.SetTitle(text)
}

// RunUntil is the reserved interface for task 007 (eval / until-goal): it
// will run Loop repeatedly against a verifier until a goal predicate passes,
// the budget is exhausted, or ctx is cancelled. Intentionally unimplemented
// here — the verifier contract does not exist until the eval suite lands.
//
// Suggested shape (final signature TBD in 007):
//
//	func (engine *Engine) RunUntil(
//		ctx context.Context,
//		goal func(snapshot) bool,
//		maxAttempts int,
//	) (bool, error)
func (engine *Engine) maybeCompact(
	ctx context.Context,
	yield func(session.Event, error) bool,
	usage int,
) error {
	settings := compaction.DefaultSettings()
	if engine.client == nil || !compaction.ShouldCompact(usage, engine.contextWindow, settings) {
		return nil
	}
	prep, err := compaction.PrepareCompact(engine.session.PathEntries(), settings)
	if err != nil {
		return err
	}
	if prep.FirstKeptEntryId == "" {
		return nil
	}

	id := fmt.Sprintf("compaction-%d", time.Now().UnixNano())
	if !yield(session.CompactionStarted{}, nil) {
		return context.Canceled
	}

	result, err := compaction.Compact(ctx, *prep, engine.client)
	if err != nil {
		_ = yield(session.CompactionComplete{ID: id, Failed: true}, nil)
		return err
	}
	if err := engine.session.AppendCompaction(session.Compaction{
		Summary:          result.Summary,
		FirstKeptEntryID: result.FirstKeptEntryID,
		TokensBefore:     result.TokensBefore,
		Details:          result.Details,
	}); err != nil {
		_ = yield(session.CompactionComplete{ID: id, Failed: true}, nil)
		return err
	}
	if !yield(session.CompactionComplete{ID: id}, nil) {
		return context.Canceled
	}
	engine.extensions.EmitSessionCompact("auto")
	return nil
}

func (engine *Engine) streamTurn(
	ctx context.Context,
	yield func(session.Event, error) bool,
	messages []llm.Message,
) (llm.Message, session.Event, bool) {
	id := fmt.Sprintf("assistant-%d", time.Now().UnixNano())
	var thinking, text string
	var final llm.Message
	gotDone := false
	streamFailure := "Assistant response interrupted"
	defer func() {
		if gotDone {
			return
		}
		kind, message := "error", streamFailure
		if ctx.Err() != nil {
			kind, message = "cancelled", "Assistant response cancelled"
		}
		if text != "" {
			message += "\n" + text
		}
		if thinking != "" {
			message += "\nThinking:\n" + thinking
		}
		if err := engine.session.AppendHistory(session.HistoryEntry{Kind: kind, Text: message}); err != nil {
			debuglog.Logf("save interrupted assistant history: %v", err)
		}
	}()

	for event, err := range engine.client.Stream(ctx, messages) {
		if err != nil {
			streamFailure = err.Error()
			if thinking != "" || text != "" {
				_ = yield(emitMessage(id, session.StateError, session.StopNone, thinking, text, nil, llm.Usage{}), nil)
			}
			yield(nil, err)
			return llm.Message{}, nil, false
		}

		switch event.Type {
		case llm.StreamEventTypeError:
			errText := event.Err
			if errText == "" {
				errText = "stream error"
			}
			streamFailure = errText
			yield(nil, fmt.Errorf("%s", errText))
			return llm.Message{}, nil, false

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
				return llm.Message{}, nil, false
			}

		case llm.StreamEventTypeDone:
			if len(event.Partial.Choices) == 0 {
				streamFailure = "stream finished with no assistant choice"
				yield(nil, errors.New("agent: stream finished with no assistant choice"))
				return llm.Message{}, nil, false
			}
			final = event.Partial.Choices[0].Message
			final.Usage = event.Partial.Usage
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
			return llm.Message{}, nil, false
		}
		streamFailure = "stream closed without assistant output"
		yield(nil, errors.New("agent: stream closed without assistant output"))
		return llm.Message{}, nil, false
	}

	blocks := engine.toolCallsToBlocks(final.ToolCalls)
	reason := session.StopEndTurn
	if len(blocks) > 0 {
		reason = session.StopToolUse
	}
	complete := emitMessage(id, session.StateComplete, reason, thinking, text, blocks, final.Usage)
	return final, complete, true
}

func (engine *Engine) toolCallsToBlocks(calls []llm.ToolCall) []session.ContentBlock {
	if len(calls) == 0 {
		return nil
	}
	out := make([]session.ContentBlock, 0, len(calls))
	for _, c := range calls {
		input := c.Function.Arguments
		if d := engine.ToolDetail(c.Function.Name, c.Function.Arguments); d != "" {
			input = d
		}
		out = append(out, session.ContentBlock{
			Type:     session.BlockToolUse,
			ID:       c.ID,
			Name:     c.Function.Name,
			Input:    input,
			Complete: true,
		})
	}
	return out
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
		Usage: session.TokenUsage{
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			CachedTokens:     usage.CachedTokens(),
			TotalTokens:      usage.TotalTokens,
		},
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
