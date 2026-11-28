package llm

import (
	"encoding/json"

	"github.com/pulseaiclub/phi/internal/core"
)

// MarshalToolParams encodes tool parameters for provider wire formats. empty
// is used when Params is nil or fails to encode: Anthropic wants "{}", Gemini
// wants `{"type":"object"}`, OpenAI chat wants "null", Responses wants "" so
// omitempty drops the field. Domain types carry no JSON tags, so the schema
// keys are spelled out here once for every provider.
func MarshalToolParams(params *core.FunctionParameters, empty string) json.RawMessage {
	if params == nil {
		return json.RawMessage(empty)
	}
	raw, err := json.Marshal(struct {
		Type       string      `json:"type"`
		Properties core.Object `json:"properties"`
		Required   []string    `json:"required,omitempty"`
	}{params.Type, params.Properties, params.Required})
	if err != nil {
		return json.RawMessage(empty)
	}
	return raw
}

// AssistantDone builds the terminal stream event for an assistant turn.
func AssistantDone(content, reasoning string, tools []core.ToolCall, usage core.Usage) core.StreamEvent {
	return core.StreamEvent{
		Type: core.StreamEventTypeDone,
		Final: &core.Message{
			Role:             core.RoleAssistant,
			Content:          content,
			ReasoningContent: reasoning,
			ToolCalls:        tools,
			Usage:            usage,
		},
	}
}
