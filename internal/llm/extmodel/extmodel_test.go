package extmodel

import (
	"context"
	"encoding/json"
	"iter"
	"testing"

	"github.com/pulseaiclub/phi/ext/go/pxb"
	"github.com/pulseaiclub/phi/internal/llm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func deltaEvents(seq iter.Seq2[llm.StreamEvent, error]) ([]llm.StreamEvent, error) {
	var out []llm.StreamEvent
	for ev, err := range seq {
		if err != nil {
			return out, err
		}
		out = append(out, ev)
		if ev.Type == llm.StreamEventTypeDone {
			return out, nil
		}
	}
	return out, nil
}

func TestStreamAssemblesFinalMessage(t *testing.T) {
	frames := []pxb.ModelStreamEvent{
		{Type: "delta", Reasoning: "thin"},
		{Type: "delta", Reasoning: "king", Content: "hel"},
		{Type: "delta", Content: "lo"},
		{Type: "delta", ToolCalls: mustJSON(t, []llm.ToolCall{
			{Index: 0, ID: "call1", Type: "function", Function: llm.Function{Name: "read", Arguments: `{"pa`}},
		})},
		{Type: "delta", ToolCalls: mustJSON(t, []llm.ToolCall{
			{Index: 0, Function: llm.Function{Arguments: `th":"x"}`}},
		})},
		{Type: "done", UsageJSON: mustJSON(t, llm.Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7})},
	}
	c := New(
		"echo",
		func(context.Context, pxb.ModelInvoke) (iter.Seq2[pxb.ModelStreamEvent, error], error) {
			return sliceSeq(frames), nil
		},
		llm.ModelConfig{Think: llm.ThinkConfig{Mode: llm.High, Enabled: true}},
		nil, "sys",
	)

	events, err := deltaEvents(c.Stream(t.Context(), []llm.Message{{Role: llm.RoleUser, Content: "hi"}}))
	require.NoError(t, err)
	require.NotEmpty(t, events)

	var final llm.Message
	for _, ev := range events {
		if ev.Type == llm.StreamEventTypeDone {
			final = *ev.Final
		}
	}
	assert.Equal(t, llm.RoleAssistant, final.Role)
	assert.Equal(t, "hello", final.Content)
	assert.Equal(t, "thinking", final.ReasoningContent)
	require.Len(t, final.ToolCalls, 1)
	assert.Equal(t, "read", final.ToolCalls[0].Function.Name)
	assert.JSONEq(t, `{"path":"x"}`, final.ToolCalls[0].Function.Arguments)
	assert.Equal(t, 7, final.Usage.TotalTokens)
}

func TestStreamErrorAndProtocolViolations(t *testing.T) {
	t.Run("error frame", func(t *testing.T) {
		c := New(
			"echo",
			func(context.Context, pxb.ModelInvoke) (iter.Seq2[pxb.ModelStreamEvent, error], error) {
				return sliceSeq([]pxb.ModelStreamEvent{{Type: "error", Error: "upstream 500"}}), nil
			},
			llm.ModelConfig{},
			nil,
			"",
		)
		_, err := deltaEvents(c.Stream(t.Context(), nil))
		require.EqualError(t, err, "upstream 500")
	})

	t.Run("missing done", func(t *testing.T) {
		c := New(
			"echo",
			func(context.Context, pxb.ModelInvoke) (iter.Seq2[pxb.ModelStreamEvent, error], error) {
				return sliceSeq([]pxb.ModelStreamEvent{{Type: "delta", Content: "x"}}), nil
			},
			llm.ModelConfig{},
			nil,
			"",
		)
		_, err := deltaEvents(c.Stream(t.Context(), nil))
		require.EqualError(t, err, "extension model stream closed without done")
	})

	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		c := New(
			"echo",
			func(context.Context, pxb.ModelInvoke) (iter.Seq2[pxb.ModelStreamEvent, error], error) {
				return sliceSeq(nil), nil
			},
			llm.ModelConfig{},
			nil,
			"",
		)
		_, err := deltaEvents(c.Stream(ctx, nil))
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestCompactCollectsText(t *testing.T) {
	frames := []pxb.ModelStreamEvent{
		{Type: "delta", Content: "sum"},
		{Type: "delta", Content: "mary"},
		{Type: "done"},
	}
	c := New(
		"echo",
		func(context.Context, pxb.ModelInvoke) (iter.Seq2[pxb.ModelStreamEvent, error], error) {
			return sliceSeq(frames), nil
		},
		llm.ModelConfig{}, nil, "",
	)
	res, err := c.Compact(t.Context(), llm.CompactRequest{Prompt: "summarize"})
	require.NoError(t, err)
	assert.Equal(t, "summary", res.Text)
	assert.False(t, res.Truncated)
}

func TestBuildInvoke(t *testing.T) {
	var got pxb.ModelInvoke
	c := New(
		"echo",
		func(_ context.Context, req pxb.ModelInvoke) (iter.Seq2[pxb.ModelStreamEvent, error], error) {
			got = req
			return sliceSeq([]pxb.ModelStreamEvent{{Type: "done"}}), nil
		},
		llm.ModelConfig{Think: llm.ThinkConfig{Mode: llm.High, Enabled: true}},
		[]llm.ToolDefinition{{Name: "read", Description: "Read a file"}},
		"be brief",
	)
	_, err := deltaEvents(c.Stream(t.Context(), []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, Content: "hey", ToolCalls: []llm.ToolCall{{
			ID: "c1", Type: "function", Function: llm.Function{Name: "read", Arguments: "{}"},
		}}},
		{Role: llm.RoleTool, Content: "file body", ToolCallID: "c1"},
	}))
	require.NoError(t, err)

	assert.Equal(t, "echo", got.Model)
	assert.Equal(t, "high", got.Think)
	assert.Equal(t, "be brief", got.SystemPrompt)
	var msgs []llm.Message
	require.NoError(t, json.Unmarshal(got.MessagesJSON, &msgs))
	require.Len(t, msgs, 3)
	assert.Equal(t, "hi", msgs[0].Content)
	require.Len(t, msgs[1].ToolCalls, 1)
	assert.Equal(t, "read", msgs[1].ToolCalls[0].Function.Name)
	assert.Equal(t, "c1", msgs[2].ToolCallID)
	var tools []llm.ToolDefinition
	require.NoError(t, json.Unmarshal(got.ToolsJSON, &tools))
	require.Len(t, tools, 1)
	assert.Equal(t, "read", tools[0].Name)
}

func TestStreamAccumulatorFragments(t *testing.T) {
	acc := newStreamAccumulator()
	acc.apply(llm.StreamDelta{Content: "a", ReasoningContent: "r"})
	acc.apply(llm.StreamDelta{ToolCalls: []llm.ToolCall{
		{Index: 1, ID: "b", Type: "function", Function: llm.Function{Name: "bash", Arguments: `{"c`}},
	}})
	acc.apply(llm.StreamDelta{ToolCalls: []llm.ToolCall{
		{Index: 0, ID: "a", Type: "function", Function: llm.Function{Name: "read", Arguments: "{}"}},
		{Index: 1, Function: llm.Function{Arguments: `ommand":"ls"}`}},
	}})
	msg := acc.message()
	assert.Equal(t, "a", msg.Content)
	assert.Equal(t, "r", msg.ReasoningContent)
	require.Len(t, msg.ToolCalls, 2)
	assert.Equal(t, 0, msg.ToolCalls[0].Index)
	assert.Equal(t, "read", msg.ToolCalls[0].Function.Name)
	assert.Equal(t, "b", msg.ToolCalls[1].ID)
	assert.JSONEq(t, `{"command":"ls"}`, msg.ToolCalls[1].Function.Arguments)
}

// sliceSeq yields frames then ends without done bookkeeping — the adapter
// under test handles terminal detection.
func sliceSeq(frames []pxb.ModelStreamEvent) iter.Seq2[pxb.ModelStreamEvent, error] {
	return func(yield func(pxb.ModelStreamEvent, error) bool) {
		for _, f := range frames {
			if !yield(f, nil) {
				return
			}
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
