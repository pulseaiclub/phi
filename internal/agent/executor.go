package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

// ToolCanceledResult is returned to the model when a user cancels a tool call.
const ToolCanceledResult = "User cancelled the tool call."

// Executor runs model tool_calls against a tool registry and emits ToolData for the UI.
type Executor struct {
	registry tooldef.Registry
	cwd      string
}

// NewExecutor builds an executor.
func NewExecutor(registry tooldef.Registry) *Executor {
	return &Executor{registry: registry}
}

// Run executes tool calls and yields ToolData updates via emit.
// Returns role=tool messages for the next LLM turn (including cancel stubs).
//
// A batch whose calls all target read-only tools (Definition.Readable) runs
// concurrently — read-only calls have no side effects, so parallel execution
// is order-independent. Any write-capable or unknown call falls back to
// sequential execution. Results always come back in call order.
func (e *Executor) Run(ctx context.Context, calls []llm.ToolCall, emit func(session.ToolData) bool) []llm.Message {
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

// runSequential executes calls one at a time.
func (e *Executor) runSequential(
	ctx context.Context,
	calls []llm.ToolCall,
	emit func(session.ToolData) bool,
) []llm.Message {
	results := make([]llm.Message, 0, len(calls))
	for _, call := range calls {
		if ctx.Err() != nil {
			results = append(results, e.cancelResult(call, emit))
			continue
		}
		results = append(results, e.runOne(ctx, call, emit))
	}
	return results
}

// runConcurrent executes a read-only batch in parallel, one goroutine per
// call. UI emits are serialized so ToolData never interleaves from two
// goroutines; results are reassembled in call order.
func (e *Executor) runConcurrent(
	ctx context.Context,
	calls []llm.ToolCall,
	emit func(session.ToolData) bool,
) []llm.Message {
	outcomes := make([]llm.Message, len(calls))

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
			outcomes[i] = e.runOne(ctx, call, lockedEmit)
		})
	}
	wg.Wait()
	return outcomes
}

func (e *Executor) runOne(ctx context.Context, call llm.ToolCall, emit func(session.ToolData) bool) llm.Message {
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
		return e.toolMessage(call.ID, ToolCanceledResult)
	}

	if !ok {
		errText := fmt.Sprintf("tool '%s' not found", call.Function.Name)
		_ = emit(session.ToolData{Run: e.toolRun(call, session.ToolError, detail, errText, "")})
		return e.toolMessage(call.ID, errText)
	}

	result, err := tool.Run(tooldef.WithToolCallID(ctx, call.ID), args)

	var (
		errText string
		content string
		output  string
	)
	if err != nil {
		if ctx.Err() != nil {
			return e.cancelResult(call, emit)
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

	if err != nil {
		_ = emit(session.ToolData{Run: e.toolRun(call, session.ToolError, detail, errText, output)})
		return e.toolMessage(call.ID, content)
	}
	run := e.toolRun(call, session.ToolDone, detail, "", output)
	run.Expanded = result.Expanded
	_ = emit(session.ToolData{Run: run})
	return e.toolMessage(call.ID, content)
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
