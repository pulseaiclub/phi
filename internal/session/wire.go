package session

import (
	"encoding/json"

	"github.com/pulseaiclub/phi/internal/core"
)

// sessionMessageWire is the stable on-disk message shape. Domain types stay
// serialization-neutral so persistence changes remain explicit here.
type sessionMessageWire struct {
	Role             core.Role             `json:"role"`
	Content          string                `json:"content"`
	ReasoningContent string                `json:"reasoning_content,omitempty"`
	ToolCalls        []sessionToolCallWire `json:"tool_calls,omitempty"`
	ToolCallID       string                `json:"tool_call_id,omitempty"`
	Images           []sessionImageWire    `json:"images,omitempty"`
	ProviderState    *sessionProviderWire  `json:"provider_state,omitempty"`
}

type sessionToolCallWire struct {
	Index            int                 `json:"index,omitempty"`
	ID               string              `json:"id"`
	Type             string              `json:"type"`
	Function         sessionFunctionWire `json:"function"`
	ThoughtSignature string              `json:"thought_signature,omitempty"`
}

type sessionFunctionWire struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type sessionImageWire struct {
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

type sessionProviderWire struct {
	Provider core.Provider       `json:"provider"`
	Model    string              `json:"model"`
	Endpoint string              `json:"endpoint"`
	Data     sessionProviderData `json:"data,omitempty"`
}

type sessionProviderData struct {
	Prefix string            `json:"prefix,omitempty"`
	Items  []json.RawMessage `json:"items,omitempty"`
}

func messageToWire(message core.Message) sessionMessageWire {
	wire := sessionMessageWire{
		Role:             message.Role,
		Content:          message.Content,
		ReasoningContent: message.ReasoningContent,
		ToolCallID:       message.ToolCallID,
	}
	for _, call := range message.ToolCalls {
		wire.ToolCalls = append(wire.ToolCalls, sessionToolCallWire{
			Index: call.Index,
			ID:    call.ID,
			Type:  call.Type,
			Function: sessionFunctionWire{
				Name: call.Function.Name, Arguments: call.Function.Arguments,
			},
			ThoughtSignature: call.ThoughtSignature,
		})
	}
	for _, image := range message.Images {
		wire.Images = append(wire.Images, sessionImageWire(image))
	}
	if message.ProviderState != nil {
		wire.ProviderState = &sessionProviderWire{
			Provider: message.ProviderState.Provider,
			Model:    message.ProviderState.Model,
			Endpoint: message.ProviderState.Endpoint,
			Data: sessionProviderData{
				Prefix: message.ProviderState.Data.Prefix,
				Items:  message.ProviderState.Data.Items,
			},
		}
	}
	return wire
}

func (w sessionMessageWire) domain() core.Message {
	message := core.Message{
		Role: w.Role, Content: w.Content, ReasoningContent: w.ReasoningContent, ToolCallID: w.ToolCallID,
	}
	for _, call := range w.ToolCalls {
		message.ToolCalls = append(message.ToolCalls, core.ToolCall{
			Index: call.Index, ID: call.ID, Type: call.Type,
			Function:         core.Function{Name: call.Function.Name, Arguments: call.Function.Arguments},
			ThoughtSignature: call.ThoughtSignature,
		})
	}
	for _, image := range w.Images {
		message.Images = append(message.Images, core.Image(image))
	}
	if w.ProviderState != nil {
		message.ProviderState = &core.ProviderState{
			Provider: w.ProviderState.Provider,
			Model:    w.ProviderState.Model,
			Endpoint: w.ProviderState.Endpoint,
			Data:     core.ProviderData{Prefix: w.ProviderState.Data.Prefix, Items: w.ProviderState.Data.Items},
		}
	}
	return message
}

type sessionUsageWire struct {
	CompletionTokens    int                     `json:"completion_tokens"`
	PromptTokens        int                     `json:"prompt_tokens"`
	TotalTokens         int                     `json:"total_tokens"`
	PromptTokensDetails *sessionUsageDetailWire `json:"prompt_tokens_details,omitempty"`
}

type sessionUsageDetailWire struct {
	CachedTokens     int `json:"cached_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

func usageToWire(usage core.Usage) sessionUsageWire {
	wire := sessionUsageWire{
		CompletionTokens: usage.CompletionTokens,
		PromptTokens:     usage.PromptTokens,
		TotalTokens:      usage.TotalTokens,
	}
	if usage.PromptTokensDetails != nil {
		wire.PromptTokensDetails = &sessionUsageDetailWire{
			CachedTokens:     usage.PromptTokensDetails.CachedTokens,
			CacheWriteTokens: usage.PromptTokensDetails.CacheWriteTokens,
		}
	}
	return wire
}

func (w sessionUsageWire) domain() core.Usage {
	usage := core.Usage{CompletionTokens: w.CompletionTokens, PromptTokens: w.PromptTokens, TotalTokens: w.TotalTokens}
	if w.PromptTokensDetails != nil {
		usage.PromptTokensDetails = &core.PromptTokensDetails{
			CachedTokens:     w.PromptTokensDetails.CachedTokens,
			CacheWriteTokens: w.PromptTokensDetails.CacheWriteTokens,
		}
	}
	return usage
}
