package pxb

// Message payloads: one struct per message, one `pxb:"<tag>"` per field. This
// file is the single source of truth for the wire; `make generate` writes
// msg_gen.go from it. Do not hand-write encode/decode switches.
//
// `,opt` marks a field omitted on the wire when it is zero (false / 0), which is
// what keeps old peers forward-compatible. Empty strings, blobs, and lists are
// always omitted, so they never need `,opt`.
//
// Evolution rules:
//
//   - Assign a new tag number for every new field; never reuse a tag. Tags are
//     namespaced per message: 1-63 message-defined, 64-127 reserved
//     (cross-cutting), 128+ experimental/private (must stay skippable).
//   - Decoders skip unknown tags (Walk does this by default).
//   - Omitting a tag means the zero value (empty string / nil / false / 0).
//   - Event codes (Ev*) are append-only; never reuse a code.
//   - Incompatible renames bump ProtocolVersion and refuse old peers.
//
//go:generate go run ../internal/pxbgen

// Hello is the first frame from an extension.
type Hello struct {
	Name     string `pxb:"1"`
	Version  string `pxb:"2"`
	Caps     uint32 `pxb:"3"`
	Protocol uint16 `pxb:"4"`
}

// HelloAck is the host reply to Hello.
type HelloAck struct {
	Protocol     uint16 `pxb:"1"`
	PhiVersion   string `pxb:"2"`
	Cwd          string `pxb:"3"`
	SessionID    string `pxb:"4"`
	ExtensionDir string `pxb:"5"`
}

// RegisterCommand registers a slash command.
type RegisterCommand struct {
	Name        string `pxb:"1"`
	Description string `pxb:"2"`
	// NeedsArgs means the host should leave "/name " in the composer when
	// the user accepts the slash picker or submits the bare command, so they
	// can type arguments. false omits the wire field (backward compatible).
	NeedsArgs bool `pxb:"3,opt"`
}

// RegisterTool registers an LLM tool. SchemaJSON is opaque JSON Schema bytes.
type RegisterTool struct {
	Name        string `pxb:"1"`
	Description string `pxb:"2"`
	SchemaJSON  []byte `pxb:"3"`
	// TimeoutSec is how long the host waits for this tool's ToolResult.
	// 0 omits the field (host default, currently 30s). Host clamps to a max.
	TimeoutSec uint32 `pxb:"4,opt"`
	// HasDetail means the extension can answer TypeToolDetailInvoke.
	// False omits the wire field (backward compatible with old hosts).
	HasDetail bool `pxb:"5,opt"`
	// Readable marks a side-effect-free tool: the host may run a batch of
	// Readable calls concurrently. False omits the wire field (old hosts
	// see the tool as non-readable and stay sequential).
	Readable bool `pxb:"6,opt"`
}

// RegisterModel registers a model the host can switch to. Conn picks the
// connection method: empty means direct-connect — the host dials BaseURL
// itself with these settings, like a models[] entry in config.yaml; "ext"
// means the extension serves each request (host sends ModelInvoke and reads
// ModelStreamEvent frames), e.g. as a proxy. API names a wire format
// ("OpenAI" | "OpenAIResponses" | "Anthropic" | "Gemini"); empty means
// OpenAI-compatible /chat/completions (direct-connect only).
type RegisterModel struct {
	Name    string `pxb:"1"`
	API     string `pxb:"2"`
	BaseURL string `pxb:"3"`
	APIKey  string `pxb:"4"`
	// Preset binds a built-in model name for capabilities and request hooks
	// (e.g. "glm-5.3-flash" for an opaque gateway route).
	Preset        string `pxb:"5"`
	ContextWindow uint32 `pxb:"6,opt"`
	ImageEnabled  bool   `pxb:"7,opt"`
	ThinkEnabled  bool   `pxb:"8,opt"`
	// ThinkLevel is off|minimal|low|medium|high|xhigh|max; empty keeps the
	// preset default.
	ThinkLevel string `pxb:"9"`
	// Conn is the connection method: "" direct-connect, "ext" extension-served.
	Conn string `pxb:"10"`
}

// ModelInvoke is host→ext for one chat completion on a Conn "ext" model.
// Messages/Tools use the session-serialization JSON shapes of llm.Message and
// llm.ToolDefinition. The invoke frame ID correlates the ModelStreamEvent
// stream; ModelCancel carries the same ID to best-effort stop it.
type ModelInvoke struct {
	Model        string `pxb:"1"`
	MessagesJSON []byte `pxb:"2"`
	ToolsJSON    []byte `pxb:"3"`
	SystemPrompt string `pxb:"4"`
	// Think is the requested think mode: off|minimal|low|medium|high|xhigh|max;
	// empty means unset.
	Think string `pxb:"5"`
}

// ModelStreamEvent is ext→host, one frame per stream event, each echoing the
// ModelInvoke frame ID. Type mirrors the host StreamEventType values
// ("delta" | "done" | "error"); a stream ends on done or error. The host
// assembles the final assistant message from the deltas, so the extension
// never sends a complete message.
type ModelStreamEvent struct {
	Type string `pxb:"1"`
	// Delta payload: Content / Reasoning are text fragments; ToolCalls is a
	// JSON array of OpenAI-style tool-call deltas (index + argument fragments).
	Content   string `pxb:"2"`
	Reasoning string `pxb:"3"`
	ToolCalls []byte `pxb:"4"`
	// UsageJSON is a JSON usage object; report it before or on the done frame.
	UsageJSON []byte `pxb:"5"`
	// Error is the failure text for Type "error".
	Error string `pxb:"6"`
}

// ToolDetailResult is ext→host for TypeToolDetailInvoke (reuse ToolInvoke body).
type ToolDetailResult struct {
	Detail string `pxb:"1"`
}

// Subscribe declares event / intercept interests.
type Subscribe struct {
	Events    []uint16 `pxb:"1"`
	Intercept []uint16 `pxb:"2"`
}

// CommandInvoked is host→ext when the user runs a slash command.
type CommandInvoked struct {
	Name string `pxb:"1"`
	Args string `pxb:"2"`
}

// CommandResponse is ext→host.
type CommandResponse struct {
	OK     bool   `pxb:"1"`
	Error  string `pxb:"2"`
	Notify string `pxb:"3"`
	Submit string `pxb:"4"`
}

// ToolInvoke is host→ext for a registered tool.
type ToolInvoke struct {
	Name string `pxb:"1"`
	Args []byte `pxb:"2"`
}

// ToolResultMsg is ext→host tool outcome.
//
//pxb:pub ToolResult
type ToolResultMsg struct {
	Content  string `pxb:"1"`
	Detail   string `pxb:"2"`
	Output   string `pxb:"3"`
	IsError  bool   `pxb:"4"`
	Error    string `pxb:"5"`
	Expanded bool   `pxb:"6,opt"` // TUI tool row starts expanded (user toggle still wins)
}

// InterceptReq is host→ext for a blocking decision point.
type InterceptReq struct {
	Event      uint16 `pxb:"1"`
	ToolName   string `pxb:"2"`
	ToolCallID string `pxb:"3"`
	Input      []byte `pxb:"4"`
	Content    string `pxb:"5"`
	IsError    bool   `pxb:"6"`
	ErrText    string `pxb:"7"`
	Prompt     string `pxb:"8"`
	Reason     string `pxb:"9"`
	TargetID   string `pxb:"10"`
	TurnIndex  uint32 `pxb:"11"`
}

// InterceptResp is ext→host.
type InterceptResp struct {
	Block              bool   `pxb:"1"`
	Stop               bool   `pxb:"2"`
	Cancel             bool   `pxb:"3"`
	Reason             string `pxb:"4"`
	Input              []byte `pxb:"5"`
	Content            string `pxb:"6"`
	Context            string `pxb:"7"`
	SystemPromptAppend string `pxb:"8"`
	Toast              string `pxb:"9"`
	Handled            bool   `pxb:"10"`
	Prompt             string `pxb:"11"` // rewrite user prompt / steer message
	Continue           bool   `pxb:"12"`
}

// EventNotify is a fire-and-forget host→ext lifecycle event.
type EventNotify struct {
	Event             uint16 `pxb:"1"`
	ToolName          string `pxb:"2"`
	ToolCallID        string `pxb:"3"`
	Input             []byte `pxb:"4"`
	IsError           bool   `pxb:"5"`
	Prompt            string `pxb:"6"`
	Reason            string `pxb:"7"`
	TurnIndex         uint32 `pxb:"8"`
	SessionID         string `pxb:"9"`
	PreviousSessionID string `pxb:"10"`
	TargetSessionID   string `pxb:"11"`
}

// NotifyMsg is ext→host UI toast/status.
//
//pxb:pub Notify
type NotifyMsg struct {
	Level     string `pxb:"1"`
	Message   string `pxb:"2"`
	Status    string `pxb:"3"`
	StatusSet bool   `pxb:"4"`
}

// HostRequest is ext→host capability RPC.
type HostRequest struct {
	Method string `pxb:"1"` // send_user_message
	Arg    string `pxb:"2"`
}

// HostResult is host→ext reply.
type HostResult struct {
	OK    bool   `pxb:"1"`
	Error string `pxb:"2"`
	Body  string `pxb:"3"`
}

// SessionMeta is host→ext session identity push.
type SessionMeta struct {
	SessionID string `pxb:"1"`
	Cwd       string `pxb:"2"`
}
