package llm

import (
	"context"
	"encoding/json"
)

// CompactRequest is one summarization call: the prompt plus the output cap
// the provider may spend on it. MaxTokens <= 0 leaves the provider default.
type CompactRequest struct {
	Prompt    string
	MaxTokens int
}

// CompactResult is the model's summary. Truncated reports that the provider
// stopped at the output cap, so Text is a partial summary that must not be
// persisted as a session checkpoint.
type CompactResult struct {
	Text      string
	Truncated bool
}

// Compactor compresses conversation history into a concise summary.
// Implemented by *client.Client; consumed by session compaction.
type Compactor interface {
	Compact(ctx context.Context, req CompactRequest) (CompactResult, error)
}

type RouterType string

const (
	OpenAI          RouterType = "OpenAI"
	OpenAIResponses RouterType = "OpenAIResponses"
	Anthropic       RouterType = "Anthropic"
	Gemini          RouterType = "Gemini"
)

type ThinkMode string

const (
	Off     ThinkMode = "off"
	Minimal ThinkMode = "minimal"
	Low     ThinkMode = "low"
	Medium  ThinkMode = "medium"
	High    ThinkMode = "high"
	XHigh   ThinkMode = "xhigh"
	Max     ThinkMode = "max"
)

// ThinkConfig is the provider-agnostic reasoning level. Provider wire formats
// (OpenAI reasoning_effort, Gemini thinkingBudget/level, …) are applied by
// each client or model RequestInterceptor.
type ThinkConfig struct {
	Mode    ThinkMode
	Enabled bool
}

// RequestInterceptor customizes a provider request after BuildRequest and before
// the HTTP call. Req is the provider's request type (e.g. openai.Request).
// cfg is the live session ModelConfig so hooks can read Think and other fields.
type RequestInterceptor[Req any] interface {
	Before(ctx context.Context, req *Req, cfg ModelConfig) error
}

// ModelConfig is the connection config for one LLM endpoint: either an
// OpenAI-compatible endpoint or the Anthropic Messages API. It also carries
// agent-wide settings like the skill directory path.
type ModelConfig struct {
	Name    string
	APIKey  string
	BaseURL string
	// SkillPaths lists the directories scanned for SKILL.md files, in priority
	// order (earlier roots win on duplicate names). Empty disables skills.
	SkillPaths []string
	// ContextWindow is the model's context window in tokens.
	// Zero disables session compaction (safe default).
	ContextWindow int
	// MaxRounds is the tool-round budget. Zero → engine default (64).
	MaxRounds int
	// ImageEnabled opts this model into image attachments (clipboard / @file).
	// Absent or false keeps the composer from attaching images.
	ImageEnabled bool
	API          RouterType
	// Think controls reasoning effort sent as reasoning_effort.
	Think ThinkConfig
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

// ToolCall is a model-requested tool invocation. JSON tags support session
// serialization; providers build their own wire representations.
type ToolCall struct {
	Index    int      `json:"index,omitempty"`
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

// Function describes the tool name and JSON arguments.
type Function struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Image is one base64-encoded image attached to a user message.
// Data is base64 bytes plus the MIME type so each provider can build its own wire format (image_url / source).
type Image struct {
	Data     string `json:"data"`     // base64-encoded image bytes
	MimeType string `json:"mimeType"` // e.g. "image/png", "image/jpeg"
}

// PromptTokensDetails holds breakdown details for prompt token usage
// (OpenAI-compatible prompt_tokens_details).
type PromptTokensDetails struct {
	CachedTokens     int `json:"cached_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

// Usage summarizes token consumption as disjoint buckets. PromptTokens is the
// input that missed the cache; cache reads and writes are reported separately,
// so PromptTokens+CachedTokens+CacheWriteTokens is the whole prompt. Providers
// that fold cache into their input count (OpenAI, Gemini) are split apart when
// parsed, so PromptTokens never means "whole prompt" in this struct.
type Usage struct {
	CompletionTokens    int                  `json:"completion_tokens"`
	PromptTokens        int                  `json:"prompt_tokens"`
	TotalTokens         int                  `json:"total_tokens"`
	PromptTokensDetails *PromptTokensDetails `json:"prompt_tokens_details,omitempty"`
}

// CachedTokens returns cache-read tokens when the provider reported them.
func (u Usage) CachedTokens() int {
	if u.PromptTokensDetails == nil {
		return 0
	}
	return u.PromptTokensDetails.CachedTokens
}

// CacheWriteTokens returns cache-write tokens when the provider reported them.
func (u Usage) CacheWriteTokens() int {
	if u.PromptTokensDetails == nil {
		return 0
	}
	return u.PromptTokensDetails.CacheWriteTokens
}

// ContextTokens is the size of the context this completion occupied: the
// provider's total when it sent one, otherwise the sum of the buckets (they are
// disjoint, so they add up to the prompt plus the reply). The context-fill
// readout and the compaction threshold both size the window from here, so the
// two cannot disagree about how full the context is.
func (u Usage) ContextTokens() int {
	if u.TotalTokens > 0 {
		return u.TotalTokens
	}
	return u.PromptTokens + u.CompletionTokens + u.CachedTokens() + u.CacheWriteTokens()
}

// StreamDelta carries incremental content.
type StreamDelta struct {
	Role             string     `json:"role,omitempty"`
	Content          string     `json:"content,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
}

// StreamEventType categorizes stream events.
type StreamEventType string

// StreamEventType values categorize stream events.
const (
	StreamEventTypeDelta StreamEventType = "delta"
	StreamEventTypeDone  StreamEventType = "done"
	StreamEventTypeError StreamEventType = "error"
)

// StreamEvent is yielded during streaming. Usage is available on intermediate
// events when the provider reports it; Final is set only for a completed turn.
type StreamEvent struct {
	Type  StreamEventType `json:"type"`
	Delta StreamDelta     `json:"delta,omitempty"`
	Usage Usage           `json:"usage,omitempty"`
	Final *Message        `json:"final,omitempty"`
	Err   string          `json:"err,omitempty"`
}

// Object is a JSON-schema properties map.
type Object = map[string]any

// ToolDefinition describes a function tool for the model.
type ToolDefinition struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Params      *FunctionParameters `json:"parameters"`
	// Readable marks a side-effect-free tool: a batch of calls that all
	// target Readable tools may run concurrently (parallel reads are safe;
	// writes must not). Not serialized to the model.
	Readable bool `json:"-"`
}

// FunctionParameters is JSON Schema for tool params.
type FunctionParameters struct {
	Type       string   `json:"type"`
	Properties Object   `json:"properties"`
	Required   []string `json:"required,omitempty"`
}

// Message is the internal representation of one chat turn. It is provider-neutral
// and persisted in session files. JSON tags support session serialization only;
// providers build their wire formats from this structure, not from the tags.
type Message struct {
	Role             Role           `json:"role"`
	Content          string         `json:"content"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall     `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
	Images           []Image        `json:"images,omitempty"`
	ProviderState    *ProviderState `json:"provider_state,omitempty"`

	// Usage tracks token consumption for the turn. Excluded from session
	// serialization; SessionMessageEntry carries it separately.
	Usage Usage `json:"-"`
}

// ProviderState carries opaque provider-specific data needed for continuation
// (e.g., Anthropic's signed thinking blocks for extended context prompting).
// The normalized fields (Content, ReasoningContent, ToolCalls) remain the
// authoritative representation for rendering, other providers, and compaction.
type ProviderState struct {
	// Provider identifies which LLM API produced this state.
	Provider RouterType `json:"provider"`
	// Model is the model name that produced this state (e.g., "claude-3-5-sonnet").
	Model string `json:"model"`
	// Endpoint is a fingerprint of the base URL (hashed to avoid leaking credentials).
	Endpoint string `json:"endpoint"`
	// Data is the opaque provider-specific payload. For Anthropic, this holds
	// the prefix hash and content blocks; other providers may use it differently
	// or leave it nil.
	Data ProviderData `json:"data,omitempty"`
}

// ProviderData is the opaque payload inside ProviderState.
type ProviderData struct {
	// Prefix is the SHA-256 hex digest of the request prefix (system + tools + messages)
	// that the native blocks depend on. Used by Anthropic to validate cache coherence.
	Prefix string `json:"prefix,omitempty"`
	// Items holds the raw content blocks for providers that need them (e.g., Anthropic
	// thinking blocks). Other providers leave this nil.
	Items []RawJSON `json:"items,omitempty"`
}

// RawJSON is an alias for json.RawMessage to make the intent clearer.
type RawJSON = json.RawMessage
