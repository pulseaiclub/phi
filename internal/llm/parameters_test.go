package llm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestParametersValidation(t *testing.T) {
	for _, key := range []string{"model", "messages", "tools", "stream", "stream_options", "max_tokens", "max_completion_tokens", "extra_body"} {
		t.Run(key, func(t *testing.T) {
			require.ErrorContains(t, (RequestParameters{key: true}).Validate(OpenAI), key)
		})
	}
	for _, api := range []RouterType{Anthropic, Gemini, OpenAIResponses} {
		require.NoError(t, (RequestParameters{"thinking": nil}).Validate(api))
		require.NoError(t, (RequestParameters{}).Validate(api))
	}
	require.ErrorContains(t, (RequestParameters{"vendor": make(chan int)}).Validate(OpenAI), "JSON-compatible")
	require.ErrorContains(t, (RequestParameters{"thinking": nil}).Validate("Unknown"), "unsupported API")
	require.NoError(t, (RequestParameters{"extra_body": nil, "enable_thinking": false}).Validate(""))
}

func TestGeminiParametersValidation(t *testing.T) {
	for _, field := range []string{"maxOutputTokens", "max_output_tokens", "candidateCount", "candidate_count"} {
		t.Run(field, func(t *testing.T) {
			for _, value := range []any{2, nil} {
				params := RequestParameters{"generationConfig": map[string]any{field: value}}
				require.ErrorContains(t, params.Validate(Gemini), "generationConfig."+field)
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
		require.ErrorContains(t, (RequestParameters{tt.key: tt.value}).Validate(Gemini), tt.wantErr)
	}
}

func TestResponsesBackgroundParameters(t *testing.T) {
	for _, value := range []any{true, json.RawMessage(` true `)} {
		require.ErrorContains(t, (RequestParameters{"background": value}).Validate(OpenAIResponses),
			"request_parameters.background=true is unsupported")
	}
	for _, value := range []any{false, nil} {
		require.NoError(t, (RequestParameters{"background": value}).Validate(OpenAIResponses))
	}
	require.NoError(t, (RequestParameters{"background": true}).Validate(OpenAI))
}

func TestGeminiInteractionsParameters(t *testing.T) {
	for _, tt := range []struct {
		params RequestParameters
		field  string
	}{
		{RequestParameters{"previous_interaction_id": "other"}, "previous_interaction_id"},
		{RequestParameters{"continuation_token": "cursor"}, "continuation_token"},
		{RequestParameters{"last_event_id": "cursor"}, "last_event_id"},
		{RequestParameters{"generationConfig": map[string]any{}}, "use generation_config"},
		{RequestParameters{"generation_config": nil}, "must be an object"},
		{RequestParameters{"generation_config": map[string]any{"max_output_tokens": 10}}, "generation_config.max_output_tokens"},
		{RequestParameters{"store": nil}, "store must be true or false"},
		{RequestParameters{"background": "true"}, "background must be true or false"},
		{RequestParameters{"background": true, "store": false}, "requires store=true"},
	} {
		require.ErrorContains(t, tt.params.Validate(GeminiInteractions), tt.field)
	}
	require.NoError(t, (RequestParameters{"background": true}).Validate(GeminiInteractions))
	require.NoError(t, (RequestParameters{"store": false}).Validate(GeminiInteractions))
	require.NoError(t, (RequestParameters{"previous_interaction_id": "vendor-id"}).Validate(OpenAI))

	body, err := MarshalRequest(json.RawMessage(`{"generation_config":{"max_output_tokens":42}}`), ModelConfig{
		API:               GeminiInteractions,
		RequestParameters: RequestParameters{"store": false, "generation_config": map[string]any{"temperature": 0}},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"store":false,"generation_config":{"max_output_tokens":42,"temperature":0}}`, string(body))
}

func TestGeminiParameterMergeKeepsManagedLimits(t *testing.T) {
	original := json.RawMessage(`{"generationConfig":{"maxOutputTokens":64}}`)
	body, err := MarshalRequest(original, ModelConfig{
		API: Gemini, RequestParameters: RequestParameters{"generationConfig": map[string]any{"temperature": 0}},
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"generationConfig":{"maxOutputTokens":64,"temperature":0}}`, string(body))
}

func TestRequestParametersRejectChoiceCount(t *testing.T) {
	for _, value := range []any{2, 1, nil} {
		require.ErrorContains(t, (RequestParameters{"n": value}).Validate(OpenAI), "request_parameters.n")
	}
}

func TestRequestParametersMerge(t *testing.T) {
	original := json.RawMessage(
		`{"model":"custom","messages":[],"tools":[{"large":9007199254740993}],"thinking":{"type":"enabled","clear_thinking":false},"reasoning_effort":"high"}`,
	)
	params := RequestParameters{
		"thinking":         map[string]any{"clear_thinking": nil},
		"reasoning_effort": nil,
		"enable_thinking":  false,
		"temperature":      0,
		"vendor":           map[string]any{"remove": nil, "keep": []any{"a", "b"}},
	}
	body, err := MarshalRequest(original, ModelConfig{RequestParameters: params})
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
		patch RequestParameters
		want  string
	}{
		{RequestParameters{"thinking": nil}, `{}`},
		{RequestParameters{"thinking": false}, `{"thinking":false}`},
		{RequestParameters{"thinking": map[string]any{}}, `{"thinking":{"type":"enabled"}}`},
	} {
		body, err := MarshalRequest(
			json.RawMessage(`{"thinking":{"type":"enabled"}}`),
			ModelConfig{RequestParameters: tt.patch},
		)
		require.NoError(t, err)
		assert.JSONEq(t, tt.want, string(body))
	}
}
