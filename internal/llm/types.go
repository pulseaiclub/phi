package llm

import "context"

// Compactor compresses conversation history into a concise summary.
// Implemented by *client.Client; consumed by session compaction.
type Compactor interface {
	Compact(ctx context.Context, summary string) (string, error)
}

// ModelConfig is the connection config for one LLM endpoint: either an
// OpenAI-compatible endpoint or the Anthropic Messages API. It also carries
// agent-wide settings like the skill directory path.
type ModelConfig struct {
	Name     string
	APIKey   string
	BaseURL  string
	Provider Provider `json:"provider,omitempty" yaml:"provider,omitempty"`
	// SkillPath is the directory to scan for SKILL.md files.
	// Defaults to ~/.phi/skills if empty.
	SkillPath string
	// ContextWindow is the model's context window in tokens.
	// Zero disables session compaction (safe default).
	ContextWindow int
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

// ToolCall is a model-requested tool invocation.
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

// Message is one chat turn (OpenAI-compatible shape, normalized across
// providers).
type Message struct {
	Role             Role       `json:"role"`
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`

	// Usage tracks token consumption for the turn. Excluded from the API
	// request body; used by the session manager for compaction decisions.
	Usage Usage `json:"-"`
}

// PromptTokensDetails holds breakdown details for prompt token usage
// (OpenAI-compatible prompt_tokens_details).
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// Usage summarizes token consumption.
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

// Response is a completed chat completion.
type Response struct {
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice is one completion choice.
type Choice struct {
	Message Message `json:"message"`
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

// StreamEvent is yielded during streaming.
type StreamEvent struct {
	Type    StreamEventType `json:"type"`
	Delta   StreamDelta     `json:"delta,omitempty"`
	Partial Response        `json:"partial,omitempty"`
	Err     string          `json:"err,omitempty"`
}

// Object is a JSON-schema properties map.
type Object = map[string]any

// ToolDefinition describes a function tool for the model.
type ToolDefinition struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Params      *FunctionParameters `json:"parameters"`
	Readable    bool                `json:"-"`
}

// FunctionParameters is JSON Schema for tool params.
type FunctionParameters struct {
	Type       string   `json:"type"`
	Properties Object   `json:"properties"`
	Required   []string `json:"required,omitempty"`
}
