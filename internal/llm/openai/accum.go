package openai

import (
	"strings"

	"github.com/pulseaiclub/phi/internal/core"
)

type streamAccumulator struct {
	role      core.Role
	content   strings.Builder
	reasoning strings.Builder
	toolCalls map[int]*toolCallAcc
	maxIndex  int
}

type toolCallAcc struct {
	id   string
	typ  string
	name string
	args strings.Builder
}

func newStreamAccumulator() *streamAccumulator {
	return &streamAccumulator{
		toolCalls: make(map[int]*toolCallAcc),
		maxIndex:  -1,
	}
}

func (s *streamAccumulator) applyDelta(delta core.StreamDelta) {
	if delta.Role != "" {
		s.role = core.Role(delta.Role)
	}
	if delta.Content != "" {
		s.content.WriteString(delta.Content)
	}
	if delta.ReasoningContent != "" {
		s.reasoning.WriteString(delta.ReasoningContent)
	}
	for _, tc := range delta.ToolCalls {
		s.applyToolCallDelta(tc)
	}
}

func (s *streamAccumulator) applyMessage(msg *core.Message) {
	if msg == nil {
		return
	}
	if strings.TrimSpace(msg.Content) != "" {
		s.content.Reset()
		s.content.WriteString(msg.Content)
	}
	if strings.TrimSpace(msg.ReasoningContent) != "" {
		s.reasoning.Reset()
		s.reasoning.WriteString(msg.ReasoningContent)
	}
	for _, tc := range msg.ToolCalls {
		s.applyToolCallDelta(tc)
	}
}

func (s *streamAccumulator) applyToolCallDelta(tc core.ToolCall) {
	if tc.Index > s.maxIndex {
		s.maxIndex = tc.Index
	}
	acc := s.toolCalls[tc.Index]
	if acc == nil {
		acc = &toolCallAcc{typ: "function"}
		s.toolCalls[tc.Index] = acc
	}
	if acc.id == "" && tc.ID != "" {
		acc.id = tc.ID
	}
	if tc.Type != "" {
		acc.typ = tc.Type
	}
	if tc.Function.Name != "" {
		acc.name = tc.Function.Name
	}
	if tc.Function.Arguments != "" {
		acc.args.WriteString(tc.Function.Arguments)
	}
}

func (s *streamAccumulator) message() core.Message {
	role := s.role
	if role == "" {
		role = core.RoleAssistant
	}
	return core.Message{
		Role:             role,
		Content:          s.content.String(),
		ReasoningContent: s.reasoning.String(),
		ToolCalls:        s.orderedToolCalls(),
	}
}

func (s *streamAccumulator) orderedToolCalls() []core.ToolCall {
	if s.maxIndex < 0 {
		return nil
	}
	out := make([]core.ToolCall, 0, s.maxIndex+1)
	for i := 0; i <= s.maxIndex; i++ {
		acc, ok := s.toolCalls[i]
		if !ok {
			continue
		}
		out = append(out, core.ToolCall{
			Index: i,
			ID:    acc.id,
			Type:  acc.typ,
			Function: core.Function{
				Name:      acc.name,
				Arguments: acc.args.String(),
			},
		})
	}
	return out
}
