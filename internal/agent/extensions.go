package agent

import (
	"context"
	"encoding/json"
	"iter"

	"github.com/pulseaiclub/phi/ext/go/pxb"
	"github.com/pulseaiclub/phi/internal/extension/core"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

// Extensions is the runner surface the engine loop consumes: extension tools
// and models, the tool-call dispatch hooks around Executor.Run, and the loop
// events. The production implementation is *core.Runner.
//
// The disabled state is any nil implementation, including a nil
// *core.Runner: every Runner method is a nil-receiver no-op, so engines
// never special-case "extensions off".
type Extensions interface {
	// ExtensionTools and model lookup feed engine assembly.
	ExtensionTools() []tooldef.Tool
	LookupModel(name string) (core.ModelRoute, bool)
	ModelStream(
		ctx context.Context,
		route core.ModelRoute,
		req pxb.ModelInvoke,
	) (iter.Seq2[pxb.ModelStreamEvent, error], error)

	// PreTool / PostTool wrap tool execution (ExtensionPre → Run → ExtensionPost).
	PreTool(
		ctx context.Context,
		toolName, toolCallID string,
		input json.RawMessage,
	) (json.RawMessage, bool, string, string)
	PostTool(
		ctx context.Context,
		toolName, toolCallID string,
		input json.RawMessage,
		content string,
		isError bool,
		errText string,
	) (string, string, bool, string)
	EmitToolExecutionStart(toolName, id string, args json.RawMessage)
	EmitToolExecutionEnd(toolName, id string, isError bool)

	// Loop events.
	EmitUserInput(text string) (out string, handled bool)
	EmitBeforeAgentStart(prompt string) (newPrompt, appendText string)
	EmitAgentStart()
	EmitAgentEnd()
	EmitTurnStart(idx int)
	EmitTurnEnd(idx int)
	EmitSessionCompact(reason string)
}

// disabledExtensions is the nil Extensions every engine assembles against, so
// a missing runner keeps the nil-receiver semantics of the concrete one.
func disabledExtensions() Extensions {
	return (*core.Runner)(nil)
}
