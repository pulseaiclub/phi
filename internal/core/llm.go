package core

import "encoding/json"

// Object is a JSON-schema properties map.
type Object = map[string]any

// ToolDefinition describes a function tool exposed to a model.
type ToolDefinition struct {
	Name        string
	Description string
	Params      *FunctionParameters
	// Readable marks a side-effect-free tool: a batch of calls that all
	// target Readable tools may run concurrently (parallel reads are safe;
	// writes must not). Not serialized to the model.
	Readable bool
}

// FunctionParameters describes the JSON object accepted by a function tool.
type FunctionParameters struct {
	Type       string
	Properties Object
	Required   []string
}

// Role identifies the participant in a chat message.
type Role string

// Role values identify the participant in a chat message.
const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Provider identifies the model provider that owns provider state.
type Provider string

// Provider values identify the supported provider families.
const (
	ProviderOpenAI          Provider = "OpenAI"
	ProviderOpenAIResponses Provider = "OpenAIResponses"
	ProviderAnthropic       Provider = "Anthropic"
	ProviderGemini          Provider = "Gemini"
)

// Message is the provider-neutral representation of one model message.
// Content is kept as text plus attachments for now; provider-specific replay
// data belongs in ProviderState instead of the message's public fields.
type Message struct {
	Role             Role
	Content          string
	ReasoningContent string
	ToolCalls        []ToolCall
	ToolCallID       string
	Images           []Image
	ProviderState    *ProviderState
	Usage            Usage
}

// ToolCall is a function invocation requested by the model.
type ToolCall struct {
	Index int
	ID    string
	Type  string

	Function Function

	// ThoughtSignature is an opaque provider continuation value. Gemini uses it
	// to validate replayed function calls; other providers leave it empty.
	ThoughtSignature string
}

// Function is the provider-neutral name and JSON argument document of a tool call.
type Function struct {
	Name      string
	Arguments string
}

// Image is a base64-encoded image attached to a message.
type Image struct {
	Data     string
	MimeType string
}

// ProviderState carries provider-specific continuation data. Normalized message
// fields remain authoritative for rendering, other providers, and compaction.
type ProviderState struct {
	Provider Provider
	Model    string
	Endpoint string
	Data     ProviderData
}

// ProviderData holds the provider's continuation data. Prefix binds Items to
// the request history they depend on; adapters own the interpretation.
type ProviderData struct {
	Prefix string
	Items  []json.RawMessage
}

// PromptTokensDetails breaks prompt usage into cache-read and cache-write buckets.
type PromptTokensDetails struct {
	CachedTokens     int
	CacheWriteTokens int
}

// Usage records token consumption using disjoint prompt buckets.
type Usage struct {
	CompletionTokens    int
	PromptTokens        int
	TotalTokens         int
	PromptTokensDetails *PromptTokensDetails
}

// CachedTokens returns the number of prompt tokens served from cache.
func (u Usage) CachedTokens() int {
	if u.PromptTokensDetails == nil {
		return 0
	}
	return u.PromptTokensDetails.CachedTokens
}

// CacheWriteTokens returns the number of prompt tokens written to cache.
func (u Usage) CacheWriteTokens() int {
	if u.PromptTokensDetails == nil {
		return 0
	}
	return u.PromptTokensDetails.CacheWriteTokens
}

// ContextTokens returns the total context size represented by this usage.
func (u Usage) ContextTokens() int {
	if u.TotalTokens > 0 {
		return u.TotalTokens
	}
	return u.PromptTokens + u.CompletionTokens + u.CachedTokens() + u.CacheWriteTokens()
}

// StreamDelta carries one incremental model output fragment.
type StreamDelta struct {
	Role             Role
	Content          string
	ReasoningContent string
	ToolCalls        []ToolCall
}

// StreamEventType identifies the kind of stream event.
type StreamEventType string

// Stream event types.
const (
	StreamEventTypeDelta StreamEventType = "delta"
	StreamEventTypeDone  StreamEventType = "done"
	StreamEventTypeError StreamEventType = "error"
)

// StreamEvent is one provider-neutral model stream event. Final is set only
// after the provider has completed a message.
type StreamEvent struct {
	Type  StreamEventType
	Delta StreamDelta
	Usage Usage
	Final *Message
	Err   string
}
