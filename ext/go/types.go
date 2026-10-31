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
	// Expanded asks the TUI to start the tool row open (user toggle still wins).
	Expanded bool
}

// Tool registers an LLM-callable tool.
type Tool struct {
	Name        string
	Label       string
	Description string
	// Parameters is a JSON Schema object (type/object/properties/required).
	Parameters map[string]any
	// TimeoutSec is how long the host waits for Execute to finish (RPC).
	// 0 uses the host default (30s). Values are clamped to 1–3600 by the host.
	TimeoutSec int
	// DetailFromArgs extracts a one-line TUI detail before Execute runs.
	// When set, the host RPCs the extension instead of showing raw JSON args.
	DetailFromArgs func(input json.RawMessage) string
	// Readable marks a side-effect-free tool. A batch of calls that all
	// target Readable tools may run concurrently on the host.
	Readable bool
	Execute  func(ctx context.Context, args json.RawMessage) (ToolResult, error)
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
	// NeedsArgs leaves "/name " in the composer on picker accept / bare submit
	// so the user can type arguments instead of running with an empty arg string.
	NeedsArgs bool
	Handler   func(args string, ctx *Context) error
}

// CommandEntry is a registered slash command name.
type CommandEntry struct {
	Name        string
	Description string
	NeedsArgs   bool
}

// Connection methods for Model.Conn.
const (
	ConnDirect = ""    // host dials BaseURL itself (like a config.yaml entry)
	ConnExt    = "ext" // Model.Stream serves each request (proxy / gateway / local)
)

// Model registers a model the host can switch to.
type Model struct {
	Name string
	// Conn picks the connection method: ConnExt means Stream serves each
	// request; empty (ConnDirect) means the host dials BaseURL itself.
	Conn string
	// Direct-connect fields (Conn ""). API names a wire format:
	// "OpenAI" | "OpenAIResponses" | "Anthropic" | "Gemini"; empty is
	// OpenAI-compatible chat completions.
	API     string
	BaseURL string
	APIKey  string
	// Preset binds a built-in model name for capabilities and request hooks
	// (e.g. "glm-5.3-flash" for an opaque gateway route).
	Preset string
	// ContextWindow is the model's context window in tokens (0 disables
	// compaction). ImageEnabled opts into image attachments. ThinkEnabled
	// exposes thinking; ThinkLevel (off|minimal|low|medium|high|xhigh|max)
	// defaults the level, empty keeps the preset default.
	ContextWindow int
	ImageEnabled  bool
	ThinkEnabled  bool
	ThinkLevel    string
	// Stream serves one chat completion when Conn == ConnExt. Emit deltas and
	// return nil to finish (the SDK sends done); return an error to fail the
	// stream. The host may invoke concurrently — guard shared state, and
	// respect ctx when forwarding upstream so cancellation stops work.
	Stream func(ctx context.Context, req ModelRequest, emit func(ModelEvent) error) error
}

// ModelRequest is one chat completion on a ConnExt model. Messages and Tools
// use the session-serialization JSON shapes; SystemPrompt is the composed
// agent prompt (forward it as the system role).
type ModelRequest struct {
	Model        string
	Think        string // off|minimal|low|medium|high|xhigh|max; "" = unset
	SystemPrompt string
	Messages     []ChatMessage
	Tools        []ModelTool
}

// ChatMessage is the neutral conversation message. Role is
// system|user|assistant|tool; ToolCallID pairs a tool result with its call.
type ChatMessage struct {
	Role             string         `json:"role"`
	Content          string         `json:"content"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCallSpec `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
	Images           []MessageImage `json:"images,omitempty"`
	// ProviderState is provider-continuation state (e.g. Anthropic cache
	// prefix). Forward it verbatim when the upstream protocol needs it.
	ProviderState json.RawMessage `json:"provider_state,omitempty"`
}

// ToolCallSpec is a model-requested tool invocation in a request message, or
// an emitted delta fragment (index + argument chunks) in a ModelEvent.
type ToolCallSpec struct {
	Index    int          `json:"index,omitempty"`
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionSpec `json:"function"`
}

// FunctionSpec names a tool and carries its JSON arguments.
type FunctionSpec struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// MessageImage is one base64 image on a user message.
type MessageImage struct {
	Data     string `json:"data"` // base64-encoded image bytes
	MimeType string `json:"mimeType"`
}

// ModelTool is one tool schema the model may call.
type ModelTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// ModelEvent is one streamed delta. The host assembles the assistant message
// from fragments, so emit increments — never the full text. Send Usage once,
// on a delta or just before returning, for context-window accounting.
type ModelEvent struct {
	Content   string // text fragment
	Reasoning string // reasoning fragment
	// ToolCalls are OpenAI-style fragments: the first fragment for an index
	// carries ID/Type/Name, later ones append Arguments text.
	ToolCalls []ToolCallSpec
	Usage     *Usage
}

// Usage mirrors the host's disjoint token buckets.
type Usage struct {
	CompletionTokens    int                  `json:"completion_tokens"`
	PromptTokens        int                  `json:"prompt_tokens"`
	TotalTokens         int                  `json:"total_tokens"`
	PromptTokensDetails *PromptTokensDetails `json:"prompt_tokens_details,omitempty"`
}

// PromptTokensDetails splits cache traffic out of the prompt count.
type PromptTokensDetails struct {
	CachedTokens     int `json:"cached_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
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
