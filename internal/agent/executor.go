package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
	"github.com/pulseaiclub/phi/internal/util"
)

// ToolCanceledResult is returned to the model when a user cancels a tool call.
const ToolCanceledResult = "User cancelled the tool call."

const (
	extContextOpen  = "<ext_context>"
	extContextClose = "</ext_context>"
)

// Executor runs model tool_calls against a tool registry and emits ToolData for the UI.
type Executor struct {
	registry  tooldef.Registry
	gate      permission.Gate
	ask       permission.AskFunc
	ext       *extension.Runner // nil = disabled; methods are nil-safe no-ops
	sessionID string
	cwd       string
	// askMu serializes approval prompts: a concurrent read-only batch can
	// otherwise pop multiple dialogs / interleave stdin reads at once.
	askMu sync.Mutex
}

// NewExecutor builds an executor. extRunner may be nil.
func NewExecutor(
	registry tooldef.Registry,
	gate permission.Gate,
	ask permission.AskFunc,
	extRunner *extension.Runner,
) *Executor {
	if gate == nil {
		gate = permission.AllowAll{}
	}
	return &Executor{registry: registry, gate: gate, ask: ask, ext: extRunner}
}

// SetMeta attaches session identity used in extension Event payloads.
func (e *Executor) SetMeta(sessionID, cwd string) {
	if e == nil {
		return
	}
	e.sessionID = sessionID
	e.cwd = cwd
	e.ext.SetMeta(sessionID, cwd)
}

// Run executes tool calls and yields ToolData updates via emit.
// Returns role=tool messages for the next LLM turn (including cancel stubs).
// stop=true means an extension PostTool asked to end the agent loop.
//
// A batch whose calls all target read-only tools (Definition.Readable) runs
// concurrently — read-only calls have no side effects, so parallel execution
// is order-independent. Any write-capable or unknown call falls back to
// sequential execution. Results always come back in call order.
func (e *Executor) Run(
	ctx context.Context,
	calls []llm.ToolCall,
	emit func(session.ToolData) bool,
) (msgs []llm.Message, stop bool, stopReason string) {
	if e.readableBatch(calls) {
		return e.runConcurrent(ctx, calls, emit)
	}
	return e.runSequential(ctx, calls, emit)
}

// readableBatch reports whether every call targets a registered read-only
// tool. Unknown tools are treated as unsafe (a typo'd name could be a write).
func (e *Executor) readableBatch(calls []llm.ToolCall) bool {
	if len(calls) < 2 {
		return false
	}
	for _, call := range calls {
		tool, ok := e.registry[call.Function.Name]
		if !ok || !tool.Definition.Readable {
			return false
		}
	}
	return true
}

// runSequential executes calls one at a time, stopping at the first halt.
func (e *Executor) runSequential(
	ctx context.Context,
	calls []llm.ToolCall,
	emit func(session.ToolData) bool,
) (msgs []llm.Message, stop bool, stopReason string) {
	results := make([]llm.Message, 0, len(calls))
	for _, call := range calls {
		if ctx.Err() != nil {
			results = append(results, e.cancelResult(call, emit))
			continue
		}
		msg, halt, reason := e.runOne(ctx, call, emit)
		results = append(results, msg)
		if halt {
			return results, true, reason
		}
	}
	return results, false, ""
}

// runConcurrent executes a read-only batch in parallel, one goroutine per
// call. UI emits are serialized so ToolData never interleaves from two
// goroutines; results are reassembled in call order. A halt ends the turn —
// later calls already ran, but since they are read-only their results are
// safe to drop.
func (e *Executor) runConcurrent(
	ctx context.Context,
	calls []llm.ToolCall,
	emit func(session.ToolData) bool,
) (msgs []llm.Message, stop bool, stopReason string) {
	type outcome struct {
		msg    llm.Message
		halt   bool
		reason string
	}
	outcomes := make([]outcome, len(calls))

	var emitMu sync.Mutex
	var stopped atomic.Bool
	lockedEmit := func(td session.ToolData) bool {
		emitMu.Lock()
		defer emitMu.Unlock()
		if stopped.Load() {
			return false
		}
		if !emit(td) {
			stopped.Store(true)
			return false
		}
		return true
	}

	var wg sync.WaitGroup
	for i, call := range calls {
		wg.Go(func() {
			msg, halt, reason := e.runOne(ctx, call, lockedEmit)
			outcomes[i] = outcome{msg: msg, halt: halt, reason: reason}
		})
	}
	wg.Wait()

	msgs = make([]llm.Message, 0, len(calls))
	for _, o := range outcomes {
		msgs = append(msgs, o.msg)
		if o.halt {
			return msgs, true, o.reason
		}
	}
	return msgs, false, ""
}

func (e *Executor) runOne(
	ctx context.Context,
	call llm.ToolCall,
	emit func(session.ToolData) bool,
) (llm.Message, bool, string) {
	ctx = tooldef.WithCwd(ctx, e.cwd)
	tool, ok := e.registry[call.Function.Name]
	args := json.RawMessage(call.Function.Arguments)
	detail := call.Function.Arguments
	if ok && tool.DetailFromArgs != nil {
		if d := tool.DetailFromArgs(args); d != "" {
			detail = d
		}
	}

	if !emit(session.ToolData{Run: e.toolRun(call, session.ToolInProgress, detail, "", "")}) {
		return e.toolMessage(call.ID, ToolCanceledResult), false, ""
	}

	if !ok {
		errText := fmt.Sprintf("tool '%s' not found", call.Function.Name)
		_ = emit(session.ToolData{Run: e.toolRun(call, session.ToolError, detail, errText, "")})
		return e.toolMessage(call.ID, errText), false, ""
	}

	e.ext.EmitToolExecutionStart(call.Function.Name, call.ID, args)

	// ExtensionPre → Gate → Run → ExtensionPost. Pre runs before permission Ask
	// so org policy can deny without prompting the user.
	var preContext string
	newArgs, blocked, reason, ctxText := e.ext.PreTool(ctx, call.Function.Name, call.ID, args)
	preContext = ctxText
	if blocked {
		if reason == "" {
			reason = "tool execution denied by extension"
		}
		reason = appendExtContext(reason, preContext)
		e.ext.EmitToolExecutionEnd(call.Function.Name, call.ID, true)
		return e.rejectResult(call, detail, reason, emit), false, ""
	}
	if len(newArgs) > 0 {
		args = newArgs
		if tool.DetailFromArgs != nil {
			if d := tool.DetailFromArgs(args); d != "" {
				detail = d
			}
		} else {
			detail = string(args)
		}
	}

	if msg, rejected := e.checkPermission(ctx, call, args, detail, emit); rejected {
		e.ext.EmitToolExecutionEnd(call.Function.Name, call.ID, true)
		return msg, false, ""
	}

	result, err := tool.Run(tooldef.WithToolCallID(ctx, call.ID), args)

	var (
		errText string
		content string
		output  string
	)
	if err != nil {
		if ctx.Err() != nil {
			e.ext.EmitToolExecutionEnd(call.Function.Name, call.ID, true)
			return e.cancelResult(call, emit), false, ""
		}
		errText = err.Error()
		content = errText
	} else {
		content = result.Content
		output = result.Output
		if output == "" {
			output = result.Content
		}
		if result.Detail != "" {
			detail = result.Detail
		}
	}

	var (
		postContext string
		postStop    bool
		postReason  string
	)
	newContent, ctxText, stop, reason := e.ext.PostTool(
		ctx,
		call.Function.Name,
		call.ID,
		args,
		content,
		err != nil,
		errText,
	)
	postContext = ctxText
	postStop = stop
	postReason = reason
	// PostTool passes content through untouched when the runner is nil or no
	// handler rewrites it; only a changed result is applied so tool Error/Output
	// never duplicate (and nil behaves like an empty runner).
	if newContent != "" && newContent != content {
		content = newContent
		output = newContent
	}
	e.ext.EmitToolExecutionEnd(call.Function.Name, call.ID, err != nil)

	modelContent := appendExtContext(content, joinExtContexts(preContext, postContext))

	if err != nil {
		_ = emit(session.ToolData{Run: e.toolRun(call, session.ToolError, detail, errText, output)})
		return e.toolMessage(call.ID, modelContent), postStop, postReason
	}
	run := e.toolRun(call, session.ToolDone, detail, "", output)
	run.Expanded = result.Expanded
	_ = emit(session.ToolData{Run: run})
	return e.toolMessage(call.ID, modelContent), postStop, postReason
}

func (e *Executor) checkPermission(
	ctx context.Context,
	call llm.ToolCall,
	args json.RawMessage,
	detail string,
	emit func(session.ToolData) bool,
) (llm.Message, bool) {
	req, err := e.registry.PermissionRequest(call.Function.Name, args, e.cwd)
	if err != nil {
		reason := fmt.Sprintf("permission check failed: %v", err)
		return e.rejectResult(call, detail, reason, emit), true
	}

	dec, reason := e.gate.Check(ctx, req)
	switch dec {
	case permission.Allow:
		return llm.Message{}, false
	case permission.Deny:
		if reason == "" {
			reason = "tool execution denied by permissions"
		}
		return e.rejectResult(call, detail, reason, emit), true
	case permission.Ask:
		if e.ask == nil {
			if reason == "" {
				reason = "tool requires approval but no ask handler is configured"
			}
			return e.rejectResult(call, detail, reason, emit), true
		}
		res, askErr := func() (permission.AskResult, error) {
			e.askMu.Lock()
			defer e.askMu.Unlock()
			return e.ask(ctx, req, reason)
		}()
		if askErr != nil {
			msg := fmt.Sprintf("approval failed: %v", askErr)
			return e.rejectResult(call, detail, msg, emit), true
		}
		if !res.Approved {
			msg := "tool execution rejected by user"
			if res.Feedback != "" {
				msg = "This tool call was rejected by the user with feedback: " + res.Feedback
			}
			return e.rejectResult(call, detail, msg, emit), true
		}
		return llm.Message{}, false
	default:
		return e.rejectResult(call, detail, "unknown permission decision", emit), true
	}
}

func (e *Executor) rejectResult(
	call llm.ToolCall,
	detail, reason string,
	emit func(session.ToolData) bool,
) llm.Message {
	_ = emit(session.ToolData{Run: e.toolRun(call, session.ToolRejected, detail, reason, "")})
	return e.toolMessage(call.ID, reason)
}

func (e *Executor) cancelResult(call llm.ToolCall, emit func(session.ToolData) bool) llm.Message {
	detail := call.Function.Arguments
	if tool, ok := e.registry[call.Function.Name]; ok && tool.DetailFromArgs != nil {
		if d := tool.DetailFromArgs(json.RawMessage(call.Function.Arguments)); d != "" {
			detail = d
		}
	}
	_ = emit(session.ToolData{Run: e.toolRun(call, session.ToolCancelled, detail, "", ToolCanceledResult)})
	return e.toolMessage(call.ID, ToolCanceledResult)
}

// toolRun builds a ToolData payload with Name always set so headless JSONL
// and stderr logs never omit toolName.
func (*Executor) toolRun(
	call llm.ToolCall,
	status session.ToolStatus,
	detail, errText, output string,
) session.ToolRun {
	return session.ToolRun{
		ToolUseID: call.ID,
		Name:      call.Function.Name,
		Status:    status,
		Detail:    detail,
		Error:     errText,
		Output:    output,
	}
}

func (*Executor) toolMessage(id, content string) llm.Message {
	return llm.Message{
		Role:       llm.RoleTool,
		ToolCallID: id,
		Content:    content,
	}
}

func joinExtContexts(parts ...string) string {
	var nonempty []string
	for _, p := range parts {
		if p != "" {
			nonempty = append(nonempty, p)
		}
	}
	return strings.Join(nonempty, "\n\n")
}

// appendExtContext adds model-facing extension notes. TUI Detail/Output stay clean.
func appendExtContext(content, ctx string) string {
	if ctx == "" {
		return content
	}
	escaped := util.ReplaceAll(ctx, extContextClose, "</ext_context\u200b>")
	block := extContextOpen + "\n" + escaped + "\n" + extContextClose
	if content == "" {
		return block
	}
	return content + "\n\n" + block
}
