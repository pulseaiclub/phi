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
	Model          string             `json:"model"`
	MaxTokens      int                `json:"max_tokens"`
	System         []sysBlock         `json:"system,omitempty"`
	Messages       []anthropicMessage `json:"messages"`
	Stream         bool               `json:"stream"`
	Tools          []anthropicTool    `json:"tools,omitempty"`
	Thinking       *thinkingConfig    `json:"thinking,omitempty"`
	OutputConfig   *outputConfig      `json:"output_config,omitempty"`
	native         *llm.ProviderState
	nativeInput    *AnthropicRequest
	vendorThinking bool
}

type outputConfig struct {
	Effort string `json:"effort,omitempty"`
}

// ApplyVendorThinking uses budget-free GLM and DeepSeek thinking; Claude keeps budget checks.
func (r *AnthropicRequest) ApplyVendorThinking(think llm.ThinkConfig, forced, adaptive bool) {
	r.vendorThinking = true
	r.OutputConfig = nil
	r.Thinking = &thinkingConfig{Type: "disabled"}
	if forced || think.Enabled {
		r.Thinking.Type = "enabled"
		if adaptive {
			r.Thinking.Type = "adaptive"
		}
	}
	if think.Enabled {
		r.OutputConfig = &outputConfig{Effort: string(think.Mode)}
	}
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string, typed blocks, or native content
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
