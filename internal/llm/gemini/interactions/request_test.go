package interactions

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

func TestRequestToolContinuation(t *testing.T) {
	cfg := llm.ModelConfig{Name: "gemini-test", Think: llm.ThinkConfig{Enabled: true, Mode: llm.High}}
	user := llm.Message{
		Role: llm.RoleUser, Content: "read both", Images: []llm.Image{{Data: "ZGF0YQ==", MimeType: "image/png"}},
	}
	history := []llm.Message{{Role: llm.RoleSystem, Content: "project"}, user}
	tools := []llm.ToolDefinition{{Name: "read", Description: "read a file", Params: &llm.FunctionParameters{
		Type: "object", Properties: llm.Object{"path": llm.Object{"type": "string"}}, Required: []string{"path"},
	}}}
	first, err := buildRequest(cfg, "system", history, tools, true, 123)
	require.NoError(t, err)
	require.Len(t, first.Input, 1)
	assert.JSONEq(t, `{"type":"user_input","content":[{"type":"text","text":"read both"},
		{"type":"image","data":"ZGF0YQ==","mime_type":"image/png"}]}`, string(first.Input[0]))
	raw, err := json.Marshal(first)
	require.NoError(t, err)
	assert.JSONEq(t, `{"model":"gemini-test","input":[`+string(first.Input[0])+`],"stream":true,"store":true,
		"system_instruction":"system\nproject","generation_config":{
			"thinking_level":"high","thinking_summaries":"auto","max_output_tokens":123
		},"tools":[{"type":"function","name":"read","description":"read a file","parameters":{
			"type":"object","properties":{"path":{"type":"string"}},"required":["path"]
		}}]}`, string(raw))

	assistant := llm.Message{
		Role: llm.RoleAssistant, ReasoningContent: "look up",
		ToolCalls: []llm.ToolCall{
			{ID: "a", Function: llm.Function{Name: "read", Arguments: `{"path":"a"}`}},
			{ID: "b", Function: llm.Function{Name: "list", Arguments: "{}"}},
		},
	}
	items := []json.RawMessage{
		json.RawMessage(`{"type":"thought","signature":"signed","summary":[{"type":"text","text":"look up"}]}`),
		json.RawMessage(`{"type":"function_call","id":"a","name":"read","arguments":{"path":"a"}}`),
		json.RawMessage(`{"type":"function_call","id":"b","name":"list","arguments":{}}`),
	}
	assistant.ProviderState = first.providerState("ix-1", items, assistant)
	history = append(history, assistant,
		llm.Message{Role: llm.RoleTool, ToolCallID: "b", Content: ""},
		llm.Message{Role: llm.RoleTool, ToolCallID: "a", Content: "file"},
	)
	follow, err := buildRequest(cfg, "system", history, tools, true, 0)
	require.NoError(t, err)
	assert.Equal(t, "ix-1", follow.PreviousInteractionID)
	require.Len(t, follow.Input, 2)
	assert.JSONEq(t, `{"type":"function_result","call_id":"b","name":"list","result":""}`, string(follow.Input[0]))
	assert.JSONEq(t, `{"type":"function_result","call_id":"a","name":"read","result":"file"}`, string(follow.Input[1]))

	local := cfg
	local.RequestParameters = llm.RequestParameters{"store": false}
	stateless, err := buildRequest(local, "system", history, tools, true, 0)
	require.NoError(t, err)
	assert.Empty(t, stateless.PreviousInteractionID)
	require.Len(t, stateless.Input, 6)
	assert.Equal(t, items, stateless.Input[1:4])
	assert.Equal(t, follow.Input, stateless.Input[4:])
	assert.Empty(t, stateless.providerState("discard", items, assistant).Data.InteractionID)

	history[2].ProviderState = nil // A legacy session has only normalized messages.
	fallback, err := buildRequest(cfg, "system", history, tools, true, 0)
	require.NoError(t, err)
	assert.Empty(t, fallback.PreviousInteractionID)
	require.Len(t, fallback.Input, 5)
	for i, call := range items[1:] {
		assert.JSONEq(t, string(call), string(fallback.Input[i+1]))
	}
	assert.Equal(t, follow.Input, fallback.Input[3:])
}

func TestRequestContinuationBelongsToCurrentHistory(t *testing.T) {
	cfg := llm.ModelConfig{Name: "a", BaseURL: "https://test.example/v1beta"}
	user := llm.Message{Role: llm.RoleUser, Content: "old"}
	first, err := buildRequest(cfg, "sys", []llm.Message{user}, nil, true, 0)
	require.NoError(t, err)
	assistant := llm.Message{Role: llm.RoleAssistant, Content: "answer"}
	assistant.ProviderState = first.providerState("ix", []json.RawMessage{
		json.RawMessage(`{"type":"thought","signature":"private"}`),
		json.RawMessage(`{"type":"model_output","content":[{"type":"text","text":"answer"}]}`),
	}, assistant)
	history := []llm.Message{user, assistant, {Role: llm.RoleUser, Content: "next"}}
	for _, change := range []string{
		"equivalent endpoint", "system and tools", "endpoint", "model", "provider", "user", "assistant", "later assistant",
	} {
		t.Run(change, func(t *testing.T) {
			config := cfg
			msgs := slices.Clone(history)
			system := "sys"
			var tools []llm.ToolDefinition
			switch change {
			case "equivalent endpoint":
				config.BaseURL = "https://test.example/"
			case "endpoint":
				config.BaseURL = "https://other.example/v1beta"
			case "model":
				config.Name = "b"
			case "provider":
				state := *msgs[1].ProviderState
				state.Provider = llm.Gemini
				msgs[1].ProviderState = &state
			case "system and tools":
				system, tools = "new", []llm.ToolDefinition{{Name: "read"}}
			case "user":
				msgs[0].Content = "edited"
			case "assistant":
				msgs[1].Content = "edited"
			case "later assistant":
				msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, Content: "later"},
					llm.Message{Role: llm.RoleUser, Content: "again"})
			}
			r, err := buildRequest(config, system, msgs, tools, true, 0)
			require.NoError(t, err)
			if change == "equivalent endpoint" || change == "system and tools" {
				assert.Equal(t, "ix", r.PreviousInteractionID)
				assert.Equal(t, system, r.SystemInstruction)
				if change == "system and tools" {
					require.Len(t, r.Tools, 1)
					assert.Equal(t, "read", r.Tools[0].Name)
				}
				assert.Len(t, r.Input, 1)
				return
			}
			assert.Empty(t, r.PreviousInteractionID)
			if change == "later assistant" {
				// Older valid local steps remain replayable even though their remote ID is stale.
				assert.Len(t, r.Input, len(msgs)+1)
				return
			}
			assert.Len(t, r.Input, len(msgs))
			for _, step := range r.Input {
				assert.NotContains(t, string(step), "private")
			}
		})
	}
}
