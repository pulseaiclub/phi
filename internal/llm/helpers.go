package llm

import "github.com/pulseaiclub/phi/internal/core"

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
