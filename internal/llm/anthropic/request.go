package anthropic

import (
	"encoding/json"

	"github.com/pulseaiclub/phi/internal/llm"
)

type cacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

// thinkingConfig controls Anthropic's extended thinking feature.
// Type "adaptive" lets the model decide how much to think;
// Type "enabled" with BudgetTokens gives a fixed token budget.
type thinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens *int   `json:"budget_tokens,omitempty"`
}

type AnthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    []sysBlock         `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Stream    bool               `json:"stream"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Thinking  *thinkingConfig    `json:"thinking,omitempty"`
	// nativeEndpoint records where replayed thinking history came from; source
	// matching happens during BuildRequest, before hooks run.
	nativeEndpoint string
	// thinkingRequested records the config-level thinking decision at BuildRequest
	// time. A hook that clears Thinking leaves it set, so prepareNative can tell
	// "no explicit thinking config" apart from an explicitly disabled request;
	// only the latter filters thinking blocks from the wire.
	thinkingRequested bool
	// Keep provenance until Stream can check the prefix after request hooks.
	nativeMessages map[int]*llm.NativeState
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string, []anthropicContentBlock, or native []json.RawMessage
}

type anthropicTool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	CacheControl *cacheControl   `json:"cache_control,omitempty"`
}

type sysBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

// anthropicImageSource is the base64 source inside an image content block.
type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// anthropicContentBlock represents a single content block inside an Anthropic message.
type anthropicContentBlock struct {
	Type         string                `json:"type"`
	Text         string                `json:"text,omitempty"`
	ID           string                `json:"id,omitempty"`
	Name         string                `json:"name,omitempty"`
	Input        json.RawMessage       `json:"input,omitempty"`
	ToolUseID    string                `json:"tool_use_id,omitempty"`
	Content      string                `json:"content,omitempty"`
	Source       *anthropicImageSource `json:"source,omitempty"`
	CacheControl *cacheControl         `json:"cache_control,omitempty"`
}
