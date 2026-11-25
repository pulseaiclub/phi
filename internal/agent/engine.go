package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"time"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/llm/skills"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/session/compaction"
)

// Engine drives the agent loop: stream → tools → stream…
// and yields session.Event for the TUI reducer. Context compaction is owned
// here so Session stays a thin message store.
type Engine struct {
	client   llm.ChatClient
	executor *Executor
	modelCfg llm.ModelConfig
	session  *Session
}

// NewEngine runs the supplied client and executor against a session.
func NewEngine(model llm.ModelConfig, sess *Session, client llm.ChatClient, executor *Executor) (*Engine, error) {
	if sess == nil {
		return nil, errors.New("agent: session is required")
	}
	if client == nil {
		return nil, errors.New("agent: chat client is required")
	}
	if executor == nil {
		return nil, errors.New("agent: tool executor is required")
	}
	return &Engine{modelCfg: model, session: sess, client: client, executor: executor}, nil
}

// LoopOpts configures a single agent loop turn.
type LoopOpts struct {
	// PendingSkills are skill names the user selected in the composer.
	// When set, the model is instructed to read those SKILL.md files first.
	PendingSkills []string
	// Images are base64 attachments from the composer pending queue.
	Images []llm.Image
	// Resume continues inference from the current session without appending a
	// new user message or running user-input preparation hooks.
	Resume bool
	// OnPromptAppended is called synchronously after the initial user message
	// is appended successfully. It is not called for resumed loops.
	OnPromptAppended func()
}

// Loop appends the user prompt and runs inference + tool rounds until the
// model stops calling tools or the context is cancelled.
//
// Compaction runs in three places:
//  1. Before every request, when the estimated context crosses the threshold. A
//     turn that keeps calling tools can grow past the window while it runs, and
//     the turn-end check below never sees that.
//  2. After a finished turn (final assistant with no tool_calls), when usage
//     crosses the context-window threshold.
//  3. Mid-loop on a context-overflow API error: force-compact once, rebuild
//     context, and retry the stream. A second overflow fails closed.
func (engine *Engine) Loop(ctx context.Context, prompt string, opts LoopOpts) iter.Seq2[session.Event, error] {
	return func(yield func(session.Event, error) bool) {
		if err := ctx.Err(); err != nil {
			return
		}
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		next := yield
		stopped := false
		// Helpers may emit cleanup events after a consumer stops. Never send
		// those back into an exited range body, and cancel any remaining work.
		// Executor serializes concurrent tool emits and joins before returning.
		yield = func(ev session.Event, err error) bool {
			if stopped {
				return false
			}
			if !next(ev, err) {
				stopped = true
				cancel()
				return false
			}
			return true
		}

		content := prompt
		if !opts.Resume {
			if instr := pendingSkillsInstruction(engine.modelCfg.SkillPaths, opts.PendingSkills); instr != "" {
				if content == "" {
					content = instr
				} else {
					content = instr + "\n\n" + content
				}
			}
		}

		if !opts.Resume {
			if err := engine.session.Append(llm.Message{
				Role:    llm.RoleUser,
				Content: content,
				Images:  append([]llm.Image(nil), opts.Images...),
			}); err != nil {
				yield(nil, err)
				return
			}
			if opts.OnPromptAppended != nil {
				opts.OnPromptAppended()
			}
		}

		toolRounds := 0
		overflowRecovered := false
		for {
			if ctx.Err() != nil {
				return
			}

			// Check the threshold before every request, not just at the end of the
			// turn: a turn that keeps calling tools can cross the window while it
			// runs, and the turn-end check never sees it. Overflow recovery is the
			// only other guard, and it needs the provider to reject the request
			// first. A repeat attempt is cheap — the cut has nothing left to
			// summarize once the kept tail is all that remains — so this needs no
			// "already tried" bookkeeping.
			did, err := engine.compactIfNeeded(ctx, yield, engine.estimateContextTokens())
			if err != nil {
				yield(nil, err)
				return
			}
			if did {
				continue
			}

			msgs := engine.session.BuildContext()

			msg, completeEvent, err := engine.streamTurn(ctx, yield, msgs)
			if err != nil {
				if !overflowRecovered && llm.IsContextOverflow(err) {
					did, cerr := engine.compactNow(ctx, yield)
					if cerr == nil && did {
						overflowRecovered = true
						continue
					}
					// Keep the overflow first (IsContextOverflow still matches) and say
					// why recovery could not help.
					err = errors.Join(err, cerr)
				}
				yield(nil, err)
				return
			}
			if completeEvent == nil {
				// Cancelled or consumer stopped — no error to surface.
				return
			}

			if !yield(completeEvent, nil) {
				return
			}

			if err := engine.session.Append(msg); err != nil {
				yield(nil, err)
				return
			}

			if len(msg.ToolCalls) == 0 {
				// Turn finished — compact using this assistant's usage. The
				// threshold reads context size the same way the composer does.
				if _, err := engine.compactIfNeeded(ctx, yield, msg.Usage.ContextTokens()); err != nil {
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

// estimateContextTokens sizes the next request the way the compaction threshold
// reads it: the last reported context plus everything appended since.
func (engine *Engine) estimateContextTokens() int {
	return compaction.EstimateContextTokens(engine.session.PathEntries())
}

// Compaction reasons reported to session_compact extension handlers.
const (
	CompactAuto   = "auto"
	CompactManual = "manual"
)

// compactIfNeeded compacts only when usage crosses the model's compaction window.
func (engine *Engine) compactIfNeeded(
	ctx context.Context,
	yield func(session.Event, error) bool,
	usage int,
) (bool, error) {
	settings := compaction.DefaultSettings()
	if engine.client == nil || !compaction.ShouldCompact(usage, engine.modelCfg.CompactionWindow(), settings) {
		return false, nil
	}
	prep, err := engine.prepareCompaction(settings)
	if err != nil || prep.FirstKeptEntryId == "" {
		return false, err
	}
	return engine.runCompact(ctx, yield, prep)
}

// Compact compacts the session on demand, bypassing the context-window
// threshold. It is the manual entry point behind /compact and reuses the
// automatic path (PrepareCompact → Compact → AppendCompaction).
//
// did is false when there is nothing worth summarizing (already compacted, or
// no history outside the retained tail). Callers must report that state: a
// silent success would read as "context was reclaimed" when it was not.
func (engine *Engine) Compact(
	ctx context.Context,
	notify func(session.Event) bool,
) (did bool, err error) {
	if notify == nil {
		notify = func(session.Event) bool { return true }
	}
	return engine.compactNow(ctx, func(ev session.Event, err error) bool {
		return err == nil && notify(ev)
	})
}

// compactNow compacts without consulting the context-window threshold. Overflow
// recovery and the manual command both use this entry point.
func (engine *Engine) compactNow(
	ctx context.Context,
	yield func(session.Event, error) bool,
) (bool, error) {
	if engine.client == nil {
		return false, nil
	}
	prep, err := engine.prepareCompaction(compaction.DefaultSettings())
	if err != nil {
		return false, err
	}
	// A no-op must not burn the single overflow retry or emit a manual success.
	if prep.FirstKeptEntryId == "" ||
		(len(prep.MessagesToSummarize) == 0 && len(prep.TurnPrefixMessages) == 0) {
		return false, nil
	}
	return engine.runCompact(ctx, yield, prep)
}

func (engine *Engine) prepareCompaction(settings compaction.Settings) (*compaction.CompactionPreparation, error) {
	prep, err := compaction.PrepareCompact(engine.session.PathEntries(), settings)
	if err != nil {
		return nil, err
	}
	prep.Limits = compaction.OutputLimits{
		MaxTokens:     engine.modelCfg.MaxTokens,
		ContextWindow: engine.modelCfg.ContextWindow,
	}
	return prep, nil
}

// runCompact persists a prepared compaction entry.
func (engine *Engine) runCompact(
	ctx context.Context,
	yield func(session.Event, error) bool,
	prep *compaction.CompactionPreparation,
) (did bool, err error) {
	id := fmt.Sprintf("compaction-%d", time.Now().UnixNano())
	if !yield(session.CompactionStarted{}, nil) {
		return false, context.Canceled
	}

	// The summary was planned against this leaf; anchoring it anywhere else
	// would drop or duplicate history.
	leaf := engine.session.LastID()
	comp, err := compaction.Compact(ctx, *prep, engine.client)
	if err == nil {
		// A summary that lands after cancellation must not become the checkpoint.
		err = ctx.Err()
	}
	if err == nil && engine.session.LastID() != leaf {
		err = fmt.Errorf("compaction aborted: session advanced while summarizing (leaf is now %q)",
			engine.session.LastID())
	}
	if err != nil {
		_ = yield(session.CompactionComplete{ID: id, Failed: true}, nil)
		return false, err
	}
	entryID, err := engine.session.AppendCompaction(comp)
	if err != nil {
		_ = yield(session.CompactionComplete{ID: id, Failed: true}, nil)
		return false, err
	}
	// Report the entry ID, not the local counter: the marker row must keep its
	// identity across a resume, or its expanded state is lost on reload.
	complete := session.CompactionComplete{ID: entryID, TokensBefore: comp.TokensBefore, Summary: comp.Summary}
	if !yield(complete, nil) {
		return false, context.Canceled
	}
	return true, nil
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
func pendingSkillsInstruction(skillPaths, names []string) string {
	if len(names) == 0 {
		return ""
	}
	list := skills.LoadAll(skillPaths)
	targets := make([]string, 0, len(names))
	for _, name := range names {
		if s := skills.Find(list, name); s != nil && s.SkillFilePath != "" {
			targets = append(targets, s.SkillFilePath)
			continue
		}
		targets = append(targets, name)
	}
	return fmt.Sprintf(
		"You MUST read these skill files first with the read tool and follow them: %s. Do this immediately before responding.",
		strings.Join(targets, ", "),
	)
}
