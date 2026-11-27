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

	t.Run("stateless model switch", func(t *testing.T) {
		other := local
		other.Name = "gemini-other"
		switched, err := buildRequest(other, "system", history, tools, true, 0)
		require.NoError(t, err)
		assert.Equal(t, stateless.Input, switched.Input)
	})

	restarted, err := follow.withHistory(history, false)
	require.NoError(t, err)
	assert.Empty(t, restarted.PreviousInteractionID)
	assert.True(t, restarted.Store)
	assert.Equal(t, stateless.Input, restarted.Input)
	assert.Equal(t, "ix-1", follow.PreviousInteractionID)
	assert.Len(t, follow.Input, 2)

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

func TestRequestSkipsMalformedToolCallAndResult(t *testing.T) {
	const validArgs = `{"n":9007199254740993,"path":"a"}`
	assistant := llm.Message{
		Role: llm.RoleAssistant, Content: "reading files",
		ToolCalls: []llm.ToolCall{
			{ID: "broken", Function: llm.Function{Name: "read", Arguments: `{"path":`}},
			{ID: "valid", Function: llm.Function{Name: "read", Arguments: validArgs}},
			{ID: "list", Function: llm.Function{Name: "read", Arguments: `[]`}},
		},
		ProviderState: &llm.ProviderState{Provider: llm.OpenAIResponses},
	}
	history := []llm.Message{
		assistant,
		{Role: llm.RoleTool, ToolCallID: "valid", Content: "file"},
		{Role: llm.RoleTool, ToolCallID: "list", Content: "invalid arguments"},
		{Role: llm.RoleTool, ToolCallID: "broken", Content: "invalid arguments"},
		{Role: llm.RoleUser, Content: "continue"},
	}
	req, err := buildRequest(llm.ModelConfig{Name: "gemini"}, "", history, nil, true, 0)
	require.NoError(t, err)
	require.Empty(t, req.PreviousInteractionID)
	require.Len(t, req.Input, 4)
	assert.JSONEq(t, `{"type":"model_output","content":[{"type":"text","text":"reading files"}]}`, string(req.Input[0]))
	var call inputStep
	require.NoError(t, json.Unmarshal(req.Input[1], &call))
	assert.Equal(t, "function_call", call.Type)
	assert.Equal(t, "valid", call.ID)
	var args struct {
		N    int64
		Path string
	}
	require.NoError(t, json.Unmarshal(call.Arguments, &args))
	assert.Equal(t, int64(9007199254740993), args.N)
	assert.Equal(t, "a", args.Path)
	assert.JSONEq(t, `{"type":"function_result","call_id":"valid","name":"read","result":"file"}`, string(req.Input[2]))
	assert.JSONEq(t, `{"type":"user_input","content":[{"type":"text","text":"continue"}]}`, string(req.Input[3]))
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
			if change == "model" || change == "later assistant" {
				// Gemini can replay these native steps even when the server-side anchor is ineligible.
				assert.Len(t, r.Input, len(msgs)+1)
				assert.Equal(t, assistant.ProviderState.Data.Items, r.Input[1:3])
				return
			}
			assert.Len(t, r.Input, len(msgs))
			for _, step := range r.Input {
				assert.NotContains(t, string(step), "private")
			}
		})
	}
}

func TestRequestCompactedStepReplay(t *testing.T) {
	cfg := llm.ModelConfig{Name: "gemini", RequestParameters: llm.RequestParameters{"store": false}}
	first, err := buildRequest(cfg, "", nil, nil, true, 0)
	require.NoError(t, err)
	reply := llm.Message{Role: llm.RoleAssistant, Content: "answer"}
	items := []json.RawMessage{
		json.RawMessage(`{"type":"thought","signature":"signed"}`),
		json.RawMessage(`{"type":"model_output","content":[{"type":"text","text":"answer"}]}`),
	}
	state := first.providerState("", items, reply)
	for _, tc := range []struct {
		name           string
		legacy, edited bool
	}{
		{name: "retained"},
		{name: "edited", edited: true},
		{name: "legacy retained", legacy: true},
		{name: "legacy edited", legacy: true, edited: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			retained := *state
			retained.Data.Prefix = ""
			if tc.legacy {
				retained.Data.MessageDigest = ""
			}
			m := reply
			m.ProviderState = &retained
			if tc.edited {
				m.Content = "replacement"
			}
			messages := []llm.Message{{Role: llm.RoleUser, Content: "summary"}, m}
			r, err := buildRequest(cfg, "", messages, nil, true, 0)
			require.NoError(t, err)
			assert.Empty(t, r.PreviousInteractionID)
			if tc.edited {
				require.Len(t, r.Input, 2)
				assert.Contains(t, string(r.Input[1]), "replacement")
			} else {
				assert.Equal(t, items, r.Input[1:])
			}
		})
	}
}
