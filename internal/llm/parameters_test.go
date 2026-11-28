package llm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/core"
)

func TestRequestParametersValidation(t *testing.T) {
	for _, key := range []string{"model", "messages", "tools", "stream", "stream_options", "max_tokens", "max_completion_tokens", "extra_body"} {
		t.Run(key, func(t *testing.T) {
			require.ErrorContains(t, ValidateRequestParameters(core.RequestParameters{key: true}, core.OpenAI), key)
		})
	}
	for _, api := range []core.RouterType{core.Anthropic, core.Gemini, core.OpenAIResponses} {
		require.NoError(t, ValidateRequestParameters(core.RequestParameters{"thinking": nil}, api))
		require.NoError(t, ValidateRequestParameters(core.RequestParameters{}, api))
	}
	require.ErrorContains(
		t,
		ValidateRequestParameters(core.RequestParameters{"vendor": make(chan int)}, core.OpenAI),
		"JSON-compatible",
	)
	require.ErrorContains(
		t,
		ValidateRequestParameters(core.RequestParameters{"thinking": nil}, "Unknown"),
		"unsupported API",
	)
	require.NoError(
		t,
		ValidateRequestParameters(core.RequestParameters{"extra_body": nil, "enable_thinking": false}, ""),
	)
}

func TestGeminiParametersValidation(t *testing.T) {
	for _, field := range []string{"maxOutputTokens", "max_output_tokens", "candidateCount", "candidate_count"} {
		t.Run(field, func(t *testing.T) {
			for _, value := range []any{2, nil} {
				params := core.RequestParameters{"generationConfig": map[string]any{field: value}}
				require.ErrorContains(t, ValidateRequestParameters(params, core.Gemini), "generationConfig."+field)
			}
		})
	}
	for _, tt := range []struct {
		key     string
		value   any
		wantErr string
	}{
		{"generationConfig", nil, "must be an object"},
		{"generationConfig", false, "must be an object"},
		{"system_instruction", map[string]any{"parts": []any{}}, "system_instruction"},
		{"system_instruction", nil, "system_instruction"},
		{"generation_config", map[string]any{"temperature": 0}, "use generationConfig"},
		{"generation_config", map[string]any{"max_output_tokens": 8192}, "use generationConfig"},
		{"generation_config", nil, "use generationConfig"},
	} {
		require.ErrorContains(
			t,
			ValidateRequestParameters(core.RequestParameters{tt.key: tt.value}, core.Gemini),
			tt.wantErr,
		)
	}
}

func TestResponsesBackgroundParameters(t *testing.T) {
	for _, value := range []any{true, json.RawMessage(` true `)} {
		require.ErrorContains(
			t,
			ValidateRequestParameters(core.RequestParameters{"background": value}, core.OpenAIResponses),
			"request_parameters.background=true is unsupported",
		)
	}
	for _, value := range []any{false, nil} {
		require.NoError(t, ValidateRequestParameters(core.RequestParameters{"background": value}, core.OpenAIResponses))
	}
	require.NoError(t, ValidateRequestParameters(core.RequestParameters{"background": true}, core.OpenAI))
}

func TestGeminiParameterMergeKeepsManagedLimits(t *testing.T) {
	original := json.RawMessage(`{"generationConfig":{"maxOutputTokens":64}}`)
	body, err := MarshalRequest(original, core.ModelConfig{
		API:               core.Gemini,
		RequestParameters: core.RequestParameters{"generationConfig": map[string]any{"temperature": 0}},
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"generationConfig":{"maxOutputTokens":64,"temperature":0}}`, string(body))
}

func TestRequestParametersRejectChoiceCount(t *testing.T) {
	for _, value := range []any{2, 1, nil} {
		require.ErrorContains(
			t,
			ValidateRequestParameters(core.RequestParameters{"n": value}, core.OpenAI),
			"request_parameters.n",
		)
	}
}

func TestRequestParametersMerge(t *testing.T) {
	original := json.RawMessage(
		`{"model":"custom","messages":[],"tools":[{"large":9007199254740993}],"thinking":{"type":"enabled","clear_thinking":false},"reasoning_effort":"high"}`,
	)
	params := core.RequestParameters{
		"thinking":         map[string]any{"clear_thinking": nil},
		"reasoning_effort": nil,
		"enable_thinking":  false,
		"temperature":      0,
		"vendor":           map[string]any{"remove": nil, "keep": []any{"a", "b"}},
	}
	body, err := MarshalRequest(original, core.ModelConfig{RequestParameters: params})
	require.NoError(t, err)
	assert.JSONEq(
		t,
		`{"model":"custom","messages":[],"tools":[{"large":9007199254740993}],"thinking":{"type":"enabled"},"enable_thinking":false,"temperature":0,"vendor":{"keep":["a","b"]}}`,
		string(body),
	)
	assert.Contains(t, string(body), "9007199254740993", "untouched numbers must retain their precision")
	assert.Contains(t, string(original), `"clear_thinking":false`)
	assert.Contains(t, params["thinking"], "clear_thinking")
}

func TestRequestParametersReplaceAndDelete(t *testing.T) {
	for _, tt := range []struct {
		patch core.RequestParameters
		want  string
	}{
		{core.RequestParameters{"thinking": nil}, `{}`},
		{core.RequestParameters{"thinking": false}, `{"thinking":false}`},
		{core.RequestParameters{"thinking": map[string]any{}}, `{"thinking":{"type":"enabled"}}`},
	} {
		body, err := MarshalRequest(
			json.RawMessage(`{"thinking":{"type":"enabled"}}`),
			core.ModelConfig{RequestParameters: tt.patch},
		)
		require.NoError(t, err)
		assert.JSONEq(t, tt.want, string(body))
	}
}
