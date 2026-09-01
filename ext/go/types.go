package ext

import (
	"context"
	"encoding/json"
)

// ToolCallEvent is emitted before Gate for each tool invocation.
type ToolCallEvent struct {
	ToolName   string
	ToolCallID string
	Input      json.RawMessage
}

// ToolCallResult may block or rewrite tool input.
type ToolCallResult struct {
	Block   bool
	Reason  string
	Input   json.RawMessage // optional rewrite; empty keeps current
	Context string          // injected into model tool result only
}

// ToolResultEvent is emitted after a tool finishes (success or error).
type ToolResultEvent struct {
	ToolName   string
	ToolCallID string
	Input      json.RawMessage
	Content    string
	IsError    bool
	Err        string
}

// ToolResultResult may rewrite output / add model context.
type ToolResultResult struct {
	Content string // empty = keep
	Context string
	Stop    bool
	Reason  string
}

// ToolExecutionStartEvent / ToolExecutionEndEvent are notification-only.
type ToolExecutionStartEvent struct {
	ToolName   string
	ToolCallID string
	Args       json.RawMessage
}

type ToolExecutionEndEvent struct {
	ToolName   string
	ToolCallID string
	IsError    bool
}

// SessionStartEvent is emitted when a session becomes active.
type SessionStartEvent struct {
	Reason            string // startup | reload | new | resume | quit
	PreviousSessionID string
}

// SessionShutdownEvent is emitted when leaving the active session.
type SessionShutdownEvent struct {
	Reason          string
	TargetSessionID string
}

// SessionBeforeSwitchEvent is emitted before /new or /resume.
type SessionBeforeSwitchEvent struct {
	Reason          string // new | resume
	TargetSessionID string
}

// SessionBeforeSwitchResult may cancel the switch.
type SessionBeforeSwitchResult struct {
	Cancel bool
	Reason string
	Toast  string
}

// BeforeAgentStartEvent fires after user submit, before the agent loop.
type BeforeAgentStartEvent struct {
	Prompt string
}

// BeforeAgentStartResult may rewrite the prompt and/or append turn context.
// Prompt non-empty replaces the user prompt; SystemPromptAppend is appended
// to the user message (Phi has no per-turn system-prompt rewrite yet).
type BeforeAgentStartResult struct {
	Prompt             string
	SystemPromptAppend string
}

// UserInputEvent fires on every user submit (slash already dispatched).
type UserInputEvent struct {
	Text string
}

// UserInputResult may transform or swallow the prompt.
type UserInputResult struct {
	Handled bool   // true: do not start the agent loop
	Text    string // non-empty: replace prompt text
	Reason  string
}

// TurnStoppingEvent fires when the model ends a turn with no more tool calls.
type TurnStoppingEvent struct {
	TurnIndex int
}

// TurnStoppingResult may force another agent step (steer).
type TurnStoppingResult struct {
	Continue bool
	Message  string // injected as a user message when Continue
	Reason   string
}

// SessionCompactEvent notifies that context compaction ran.
type SessionCompactEvent struct {
	Reason string // auto | manual
}

// AgentStartEvent / AgentEndEvent wrap a Loop run.
type (
	AgentStartEvent struct{}
	AgentEndEvent   struct{}
)

// TurnStartEvent / TurnEndEvent wrap one LLM round (+ tools).
type TurnStartEvent struct {
	TurnIndex int
}
type TurnEndEvent struct {
	TurnIndex int
}

// ToolResult is returned from a custom tool Execute.
type ToolResult struct {
	Content string
	Detail  string
	Output  string
}

// Tool registers an LLM-callable tool.
type Tool struct {
	Name        string
	Label       string
	Description string
	// Parameters is a JSON Schema object (type/object/properties/required).
	Parameters map[string]any
	Execute    func(ctx context.Context, args json.RawMessage) (ToolResult, error)
}

// ToolInfo describes a configured tool for GetAllTools.
type ToolInfo struct {
	Name        string
	Description string
	Source      string // builtin | extension
}

// Command registers a slash command.
type Command struct {
	Description string
	Handler     func(args string, ctx *Context) error
}

// CommandEntry is a registered slash command name.
type CommandEntry struct {
	Name        string
	Description string
}

// ExecResult is returned from API.Exec.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// SessionEffects aggregates UI signals from session handlers.
type SessionEffects struct {
	Toast     string
	Status    string
	StatusSet bool
	Denied    bool
	Reason    string
}
