package extmodel

import (
	"encoding/json"

	"github.com/pulseaiclub/phi/internal/core"
)

// messageJSON is the stable session/extension representation of a domain message.
type messageJSON struct {
	Role             core.Role          `json:"role"`
	Content          string             `json:"content"`
	ReasoningContent string             `json:"reasoning_content,omitempty"`
	ToolCalls        []toolCallJSON     `json:"tool_calls,omitempty"`
	ToolCallID       string             `json:"tool_call_id,omitempty"`
	Images           []imageJSON        `json:"images,omitempty"`
	ProviderState    *providerStateJSON `json:"provider_state,omitempty"`
}

type toolCallJSON struct {
	Index            int          `json:"index,omitempty"`
	ID               string       `json:"id"`
	Type             string       `json:"type"`
	Function         functionJSON `json:"function"`
	ThoughtSignature string       `json:"thought_signature,omitempty"`
}

type functionJSON struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type imageJSON struct {
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

type providerStateJSON struct {
	Provider core.Provider `json:"provider"`
	Model    string        `json:"model"`
	Endpoint string        `json:"endpoint"`
	Data     providerData  `json:"data,omitempty"`
}

type providerData struct {
	Prefix string            `json:"prefix,omitempty"`
	Items  []json.RawMessage `json:"items,omitempty"`
}

// messageToJSON converts a domain message to its stable JSON representation.
func messageToJSON(message core.Message) messageJSON {
	out := messageJSON{
		Role:             message.Role,
		Content:          message.Content,
		ReasoningContent: message.ReasoningContent,
		ToolCallID:       message.ToolCallID,
	}
	for _, call := range message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, toolCallToJSON(call))
	}
	for _, image := range message.Images {
		out.Images = append(out.Images, imageJSON{Data: image.Data, MimeType: image.MimeType})
	}
	if state := message.ProviderState; state != nil {
		out.ProviderState = &providerStateJSON{
			Provider: state.Provider,
			Model:    state.Model,
			Endpoint: state.Endpoint,
			Data:     providerData{Prefix: state.Data.Prefix, Items: state.Data.Items},
		}
	}
	return out
}

// messageFromJSON converts a stable JSON representation to a domain message.
func messageFromJSON(message messageJSON) core.Message {
	out := core.Message{
		Role:             message.Role,
		Content:          message.Content,
		ReasoningContent: message.ReasoningContent,
		ToolCallID:       message.ToolCallID,
	}
	for _, call := range message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, toolCallFromJSON(call))
	}
	for _, image := range message.Images {
		out.Images = append(out.Images, core.Image{Data: image.Data, MimeType: image.MimeType})
	}
	if state := message.ProviderState; state != nil {
		out.ProviderState = &core.ProviderState{
			Provider: state.Provider,
			Model:    state.Model,
			Endpoint: state.Endpoint,
			Data:     core.ProviderData{Prefix: state.Data.Prefix, Items: state.Data.Items},
		}
	}
	return out
}

// marshalMessages encodes domain messages for the extension boundary.
func marshalMessages(messages []core.Message) ([]byte, error) {
	wire := make([]messageJSON, 0, len(messages))
	for _, message := range messages {
		wire = append(wire, messageToJSON(message))
	}
	return json.Marshal(wire)
}

func toolCallToJSON(call core.ToolCall) toolCallJSON {
	return toolCallJSON{
		Index:            call.Index,
		ID:               call.ID,
		Type:             call.Type,
		Function:         functionJSON{Name: call.Function.Name, Arguments: call.Function.Arguments},
		ThoughtSignature: call.ThoughtSignature,
	}
}

func toolCallFromJSON(call toolCallJSON) core.ToolCall {
	return core.ToolCall{
		Index:            call.Index,
		ID:               call.ID,
		Type:             call.Type,
		Function:         core.Function{Name: call.Function.Name, Arguments: call.Function.Arguments},
		ThoughtSignature: call.ThoughtSignature,
	}
}

// marshalUsage encodes domain usage using the stable token field names.
func marshalUsage(usage core.Usage) ([]byte, error) {
	wire := usageJSON{
		CompletionTokens: usage.CompletionTokens,
		PromptTokens:     usage.PromptTokens,
		TotalTokens:      usage.TotalTokens,
	}
	if usage.PromptTokensDetails != nil {
		wire.PromptTokensDetails = &usageDetailsJSON{
			CachedTokens:     usage.PromptTokensDetails.CachedTokens,
			CacheWriteTokens: usage.PromptTokensDetails.CacheWriteTokens,
		}
	}
	return json.Marshal(wire)
}

// unmarshalUsage decodes stable token usage into the domain type.
func unmarshalUsage(data []byte, usage *core.Usage) error {
	var wire usageJSON
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*usage = core.Usage{
		CompletionTokens: wire.CompletionTokens,
		PromptTokens:     wire.PromptTokens,
		TotalTokens:      wire.TotalTokens,
	}
	if wire.PromptTokensDetails != nil {
		usage.PromptTokensDetails = &core.PromptTokensDetails{
			CachedTokens:     wire.PromptTokensDetails.CachedTokens,
			CacheWriteTokens: wire.PromptTokensDetails.CacheWriteTokens,
		}
	}
	return nil
}

type usageJSON struct {
	CompletionTokens    int               `json:"completion_tokens"`
	PromptTokens        int               `json:"prompt_tokens"`
	TotalTokens         int               `json:"total_tokens"`
	PromptTokensDetails *usageDetailsJSON `json:"prompt_tokens_details,omitempty"`
}

type usageDetailsJSON struct {
	CachedTokens     int `json:"cached_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

// unmarshalToolCalls decodes extension tool-call fragments.
func unmarshalToolCalls(data []byte) ([]core.ToolCall, error) {
	var wire []toolCallJSON
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, err
	}
	calls := make([]core.ToolCall, 0, len(wire))
	for _, call := range wire {
		calls = append(calls, toolCallFromJSON(call))
	}
	return calls, nil
}

// marshalToolDefinitions converts domain tools to the extension wire shape.
func marshalToolDefinitions(tools []core.ToolDefinition) ([]byte, error) {
	wire := make([]struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	}, 0, len(tools))
	for _, tool := range tools {
		wire = append(wire, struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		}{tool.Name, tool.Description, marshalToolParams(tool.Params, "null")})
	}
	return json.Marshal(wire)
}

// marshalToolParams converts a domain schema to a provider JSON schema.
func marshalToolParams(params *core.FunctionParameters, empty string) json.RawMessage {
	if params == nil {
		return json.RawMessage(empty)
	}
	raw, err := json.Marshal(struct {
		Type       string      `json:"type"`
		Properties core.Object `json:"properties"`
		Required   []string    `json:"required,omitempty"`
	}{params.Type, params.Properties, params.Required})
	if err != nil || len(raw) == 0 {
		return json.RawMessage(empty)
	}
	return raw
}
