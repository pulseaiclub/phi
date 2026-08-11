package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"time"

	"github.com/pulseaiclub/phi/internal/hooks"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
	"github.com/pulseaiclub/phi/internal/llm/skills"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/session/compaction"
	"github.com/pulseaiclub/phi/internal/tools"
)

// ErrMaxRounds Engine drives the agent loop: stream → tools → stream…
// and yields session.Event for the TUI reducer. Context compaction is owned
// here so Session stays a thin message store.
// ErrMaxRounds is returned (wrapped) by Loop when the model exceeds the
// configured tool-round budget. Callers can distinguish it from other
// runtime errors with errors.Is, e.g. for a dedicated exit code.
var ErrMaxRounds = errors.New("exceeded maximum tool rounds")

type Engine struct {
	client        *llmclient.Client
	executor      *Executor
	maxRounds     int
	skillPath     string
	contextWindow int
	modelCfg      llm.ModelConfig
	gate          permission.Gate
	ask           permission.AskFunc
	jobs          *job.Manager
	hooks         *hooks.Manager

	session *Session
}

// EngineOpts configures NewEngine.
type EngineOpts struct {
	Model       llm.ModelConfig
	SessionOpts SessionOpts
	Gate        permission.Gate    // nil = allow all
	Ask         permission.AskFunc // nil = deny on Ask
	Tools       []tools.Tool       // nil = tools.DefaultTools(); sub-agents use ChildTools()
	MaxRounds   int                // 0 = package default
	Jobs        *job.Manager       // if set, register agent_* tools on this engine
	Hooks       *hooks.Manager // nil = no hooks; child engines inherit parent Manager (S9)
}

// NewEngine wires an LLM client, tool executor, and session store.
func NewEngine(opts EngineOpts) (*Engine, error) {
	sess, err := NewSession(opts.SessionOpts)
	if err != nil {
		return nil, err
	}
	cfg := opts.Model
	engine := &Engine{
		maxRounds:     defaultMaxToolRounds,
		skillPath:     cfg.SkillPath,
		contextWindow: cfg.ContextWindow,
		modelCfg:      cfg,
		session:       sess,
		gate:          opts.Gate,
		ask:           opts.Ask,
		jobs:          opts.Jobs,
		hooks:         opts.Hooks,
	}
	if opts.MaxRounds > 0 {
		engine.maxRounds = opts.MaxRounds
	}
	toolList := engine.buildToolList(opts.Tools)
	engine.client = llmclient.NewClient(cfg, tools.Definitions(toolList), Prompt(cfg.SkillPath, engine.jobs != nil))
	engine.bindExecutor(tools.NewRegistry(toolList))
	return engine, nil
}

func (engine *Engine) buildToolList(base []tools.Tool) []tools.Tool {
	if base == nil {
		base = tools.DefaultTools()
	}
	if engine.jobs == nil {
		return base
	}
	agentTools := tools.AgentTools(tools.AgentDeps{
		Manager:  engine.jobs,
		ParentID: engine.SessionID,
		WorkDir:  engine.SessionCwd,
	})
	out := make([]tools.Tool, 0, len(base)+len(agentTools))
	out = append(out, base...)
	out = append(out, agentTools...)
	return out
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
	toolList := engine.buildToolList(nil)
	engine.client = llmclient.NewClient(engine.modelCfg, tools.Definitions(toolList), Prompt(engine.skillPath, engine.jobs != nil))
	engine.bindExecutor(tools.NewRegistry(toolList))
}

func (engine *Engine) bindExecutor(registry tools.Registry) {
	engine.executor = NewExecutor(registry, engine.gate, engine.ask, engine.hooks)
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
		engine.executor.syncHookFilter()
	}
}

// SetHooks replaces the hooks manager. Pass nil to disable hooks.
// Does not drop Gate/Ask; rebindTools also preserves hooks.
func (engine *Engine) SetHooks(mgr *hooks.Manager) {
	if engine == nil {
		return
	}
	engine.hooks = mgr
	if engine.executor != nil {
		engine.executor.hooks = mgr
	}
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
func (engine *Engine) ReplaceSession(opts SessionOpts) error {
	sess, err := NewSession(opts)
	if err != nil {
		return err
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
}

// Loop appends the user prompt and runs inference + tool rounds until the
// model stops calling tools or the context is cancelled.
//
// Compaction: persist the turn first, then check usage after
// the agent turn ends (final assistant with no tool_calls) — never mid-tool-loop.
func (engine *Engine) Loop(ctx context.Context, prompt string, opts LoopOpts) iter.Seq2[session.Event, error] {
	return func(yield func(session.Event, error) bool) {
		content := prompt
		if instr := pendingSkillsInstruction(engine.skillPath, opts.PendingSkills); instr != "" {
			if content == "" {
				content = instr
			} else {
				content = instr + "\n\n" + content
			}
		}
		if err := engine.session.Append(llm.Message{
			Role:    llm.RoleUser,
			Content: content,
		}); err != nil {
			yield(nil, err)
			return
		}

		toolRounds := 0
		for {
			if ctx.Err() != nil {
				return
			}

			msgs := engine.session.BuildContext()

			msg, complete, ok := engine.streamTurn(ctx, yield, msgs)
			if !ok {
				return
			}

			// Defer publishing and persisting the terminal assistant update until
			// the tool budget is checked. An over-budget tool request must not
			// leave an unexecuted tool call in the session or UI.
			if len(msg.ToolCalls) > 0 && toolRounds >= engine.maxRounds {
				yield(nil, fmt.Errorf("agent: %w (%d)", ErrMaxRounds, engine.maxRounds))
				return
			}
			if !yield(complete, nil) {
				return
			}

			if err := engine.session.Append(msg); err != nil {
				yield(nil, err)
				return
			}

			if len(msg.ToolCalls) == 0 {
				// Turn finished — compact using this assistant's usage (pi agent_end).
				if err := engine.maybeCompact(ctx, yield, msg.Usage.TotalTokens); err != nil {
					yield(nil, err)
				}
				return
			}

			toolRounds++
			toolMsgs := engine.executor.Run(ctx, msg.ToolCalls, func(td session.ToolData) bool {
				return yield(td, nil)
			})
			if err := engine.session.Append(toolMsgs...); err != nil {
				yield(nil, err)
				return
			}

			if ctx.Err() != nil {
				return
			}
		}
	}
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

	for event, err := range engine.client.Stream(ctx, messages) {
		if err != nil {
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
			yield(nil, fmt.Errorf("%s", errText))
			return llm.Message{}, nil, false

		case llm.StreamEventTypeDelta:
			if event.Delta.ReasoningContent != "" {
				thinking += event.Delta.ReasoningContent
			}
			if event.Delta.Content != "" {
				text += event.Delta.Content
			}
			if !yield(emitMessage(id, session.StateStreaming, session.StopNone, thinking, text, nil, llm.Usage{}), nil) {
				return llm.Message{}, nil, false
			}

		case llm.StreamEventTypeDone:
			if len(event.Partial.Choices) == 0 {
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
		yield(nil, fmt.Errorf("agent: stream closed without assistant output"))
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
		if tool, ok := engine.executor.registry[c.Function.Name]; ok && tool.DetailFromArgs != nil {
			if d := tool.DetailFromArgs(json.RawMessage(c.Function.Arguments)); d != "" {
				input = d
			}
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
// selected skills (panda-style: reuse the read tool, no dedicated skill tool).
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
