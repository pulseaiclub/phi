package openai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/core"
)

func toolCallDelta(index int, id, name, args string) core.ToolCall {
	return core.ToolCall{
		Index:    index,
		ID:       id,
		Function: core.Function{Name: name, Arguments: args},
	}
}

func TestStreamAccumulatorEmpty(t *testing.T) {
	msg := newStreamAccumulator().message()

	assert.Equal(t, core.Message{Role: core.RoleAssistant}, msg)
	assert.Nil(t, msg.ToolCalls, "no tool calls must stay nil, not an empty slice")
}

func TestStreamAccumulatorTextDeltas(t *testing.T) {
	acc := newStreamAccumulator()
	for _, d := range []core.StreamDelta{
		{Role: "assistant"},
		{ReasoningContent: "Let me "},
		{ReasoningContent: "think."},
		{Content: "Hello"},
		{Content: ", "},
		{},
		{Content: "world", ReasoningContent: " Done."},
	} {
		acc.applyDelta(d)
	}

	msg := acc.message()
	assert.Equal(t, core.RoleAssistant, msg.Role)
	assert.Equal(t, "Hello, world", msg.Content)
	assert.Equal(t, "Let me think. Done.", msg.ReasoningContent)
	assert.Nil(t, msg.ToolCalls)
}

func TestStreamAccumulatorRole(t *testing.T) {
	t.Run("defaults to assistant", func(t *testing.T) {
		acc := newStreamAccumulator()
		acc.applyDelta(core.StreamDelta{Content: "hi"})
		assert.Equal(t, core.RoleAssistant, acc.message().Role)
	})

	t.Run("uses the streamed role", func(t *testing.T) {
		acc := newStreamAccumulator()
		acc.applyDelta(core.StreamDelta{Role: "tool"})
		assert.Equal(t, core.RoleTool, acc.message().Role)
	})

	t.Run("an empty role keeps the earlier one", func(t *testing.T) {
		acc := newStreamAccumulator()
		acc.applyDelta(core.StreamDelta{Role: "tool"})
		acc.applyDelta(core.StreamDelta{Content: "x"})
		assert.Equal(t, core.RoleTool, acc.message().Role)
	})

	t.Run("a later role replaces the earlier one", func(t *testing.T) {
		acc := newStreamAccumulator()
		acc.applyDelta(core.StreamDelta{Role: "tool"})
		acc.applyDelta(core.StreamDelta{Role: "assistant"})
		assert.Equal(t, core.RoleAssistant, acc.message().Role)
	})
}

func TestStreamAccumulatorToolCallFragments(t *testing.T) {
	acc := newStreamAccumulator()
	for _, d := range []core.StreamDelta{
		{Role: "assistant"},
		{ToolCalls: []core.ToolCall{toolCallDelta(0, "call_1", "read", "")}},
		{ToolCalls: []core.ToolCall{toolCallDelta(0, "", "", `{"path":`)}},
		{ToolCalls: []core.ToolCall{toolCallDelta(0, "", "", `"a.go"}`)}},
	} {
		acc.applyDelta(d)
	}

	assert.Equal(t, []core.ToolCall{{
		Index:    0,
		ID:       "call_1",
		Type:     "function",
		Function: core.Function{Name: "read", Arguments: `{"path":"a.go"}`},
	}}, acc.message().ToolCalls)
}

func TestStreamAccumulatorToolCallFields(t *testing.T) {
	t.Run("the first non-empty id wins", func(t *testing.T) {
		acc := newStreamAccumulator()
		acc.applyToolCallDelta(toolCallDelta(0, "", "read", ""))
		acc.applyToolCallDelta(toolCallDelta(0, "call_1", "", ""))
		acc.applyToolCallDelta(toolCallDelta(0, "call_2", "", ""))
		assert.Equal(t, "call_1", acc.message().ToolCalls[0].ID)
	})

	t.Run("type defaults to function", func(t *testing.T) {
		acc := newStreamAccumulator()
		acc.applyToolCallDelta(toolCallDelta(0, "call_1", "read", "{}"))
		assert.Equal(t, "function", acc.message().ToolCalls[0].Type)
	})

	t.Run("a streamed type replaces the default", func(t *testing.T) {
		acc := newStreamAccumulator()
		tc := toolCallDelta(0, "call_1", "read", "")
		tc.Type = "custom"
		acc.applyToolCallDelta(tc)
		acc.applyToolCallDelta(toolCallDelta(0, "", "", "{}"))
		assert.Equal(t, "custom", acc.message().ToolCalls[0].Type)
	})

	t.Run("a later non-empty name replaces the earlier one", func(t *testing.T) {
		acc := newStreamAccumulator()
		acc.applyToolCallDelta(toolCallDelta(0, "call_1", "rea", ""))
		acc.applyToolCallDelta(toolCallDelta(0, "", "", "{}"))
		acc.applyToolCallDelta(toolCallDelta(0, "", "read", ""))
		assert.Equal(t, "read", acc.message().ToolCalls[0].Function.Name)
	})
}

func TestStreamAccumulatorInterleavedToolCalls(t *testing.T) {
	acc := newStreamAccumulator()
	acc.applyDelta(core.StreamDelta{ToolCalls: []core.ToolCall{
		toolCallDelta(1, "call_b", "grep", `{"q":`),
		toolCallDelta(0, "call_a", "read", `{"path":`),
	}})
	acc.applyDelta(core.StreamDelta{ToolCalls: []core.ToolCall{toolCallDelta(0, "", "", `"a.go"}`)}})
	acc.applyDelta(core.StreamDelta{ToolCalls: []core.ToolCall{toolCallDelta(1, "", "", `"x"}`)}})

	assert.Equal(t, []core.ToolCall{
		{Index: 0, ID: "call_a", Type: "function", Function: core.Function{Name: "read", Arguments: `{"path":"a.go"}`}},
		{Index: 1, ID: "call_b", Type: "function", Function: core.Function{Name: "grep", Arguments: `{"q":"x"}`}},
	}, acc.message().ToolCalls)
}

func TestStreamAccumulatorToolCallIndexGap(t *testing.T) {
	acc := newStreamAccumulator()
	acc.applyToolCallDelta(toolCallDelta(2, "call_c", "find", "{}"))
	acc.applyToolCallDelta(toolCallDelta(0, "call_a", "read", "{}"))

	calls := acc.message().ToolCalls
	require.Len(t, calls, 2, "missing indexes are skipped, not filled with empty calls")
	assert.Equal(t, 0, calls[0].Index)
	assert.Equal(t, "call_a", calls[0].ID)
	assert.Equal(t, 2, calls[1].Index)
	assert.Equal(t, "call_c", calls[1].ID)
}

func TestStreamAccumulatorApplyMessage(t *testing.T) {
	t.Run("nil is a no-op", func(t *testing.T) {
		acc := newStreamAccumulator()
		acc.applyDelta(core.StreamDelta{Content: "kept", ReasoningContent: "also kept"})
		acc.applyMessage(nil)

		msg := acc.message()
		assert.Equal(t, "kept", msg.Content)
		assert.Equal(t, "also kept", msg.ReasoningContent)
	})

	t.Run("non-blank text replaces what was streamed", func(t *testing.T) {
		acc := newStreamAccumulator()
		acc.applyDelta(core.StreamDelta{Content: "Hel", ReasoningContent: "thin"})
		acc.applyMessage(&core.Message{Content: "Hello", ReasoningContent: "thinking"})

		msg := acc.message()
		assert.Equal(t, "Hello", msg.Content)
		assert.Equal(t, "thinking", msg.ReasoningContent)
	})

	t.Run("blank text keeps what was streamed", func(t *testing.T) {
		acc := newStreamAccumulator()
		acc.applyDelta(core.StreamDelta{Content: "Hello", ReasoningContent: "thinking"})
		acc.applyMessage(&core.Message{Content: " \n\t", ReasoningContent: ""})

		msg := acc.message()
		assert.Equal(t, "Hello", msg.Content)
		assert.Equal(t, "thinking", msg.ReasoningContent)
	})

	t.Run("content and reasoning are replaced independently", func(t *testing.T) {
		acc := newStreamAccumulator()
		acc.applyDelta(core.StreamDelta{Content: "Hel", ReasoningContent: "thinking"})
		acc.applyMessage(&core.Message{Content: "Hello"})

		msg := acc.message()
		assert.Equal(t, "Hello", msg.Content)
		assert.Equal(t, "thinking", msg.ReasoningContent)
	})

	t.Run("tool calls are collected", func(t *testing.T) {
		acc := newStreamAccumulator()
		acc.applyMessage(&core.Message{ToolCalls: []core.ToolCall{
			toolCallDelta(0, "call_a", "read", `{"path":"a.go"}`),
			toolCallDelta(1, "call_b", "grep", `{"q":"x"}`),
		}})

		assert.Equal(t, []core.ToolCall{
			{
				Index:    0,
				ID:       "call_a",
				Type:     "function",
				Function: core.Function{Name: "read", Arguments: `{"path":"a.go"}`},
			},
			{Index: 1, ID: "call_b", Type: "function", Function: core.Function{Name: "grep", Arguments: `{"q":"x"}`}},
		}, acc.message().ToolCalls)
	})

	t.Run("does not change the role", func(t *testing.T) {
		acc := newStreamAccumulator()
		acc.applyMessage(&core.Message{Role: core.RoleUser, Content: "x"})
		assert.Equal(t, core.RoleAssistant, acc.message().Role)
	})
}
