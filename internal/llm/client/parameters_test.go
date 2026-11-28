package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/core"
	"github.com/pulseaiclub/phi/internal/llm"
)

func TestRequestParametersFailBeforeNetwork(t *testing.T) {
	for _, cfg := range []core.ModelConfig{
		{API: core.Anthropic, RequestParameters: core.RequestParameters{"messages": nil}},
		{API: core.Gemini, RequestParameters: core.RequestParameters{"contents": nil}},
		{API: core.OpenAIResponses, RequestParameters: core.RequestParameters{"input": nil}},
		{API: core.OpenAIResponses, RequestParameters: core.RequestParameters{"background": true}},
		{API: core.OpenAI, RequestParameters: core.RequestParameters{"messages": nil}},
		{API: core.OpenAI, RequestParameters: core.RequestParameters{"n": 2}},
	} {
		c := NewClient(cfg, Hooks{}, nil, "")
		c.httpClient = nil // Any attempted network call would panic.
		seen := false
		for _, err := range c.Stream(t.Context(), nil) {
			require.ErrorContains(t, err, "request_parameters")
			seen = true
		}
		require.True(t, seen)
		_, err := c.Compact(t.Context(), llm.CompactRequest{Prompt: "hi"})
		require.ErrorContains(t, err, "request_parameters")
	}
}

func TestRequestParametersAcrossProtocols(t *testing.T) {
	for _, tt := range []struct {
		api             core.RouterType
		stream, compact string
	}{
		{
			core.OpenAI,
			"data: [DONE]\n\n",
			`{"choices":[{"message":{"content":"OK"}}]}`,
		},
		{
			core.Anthropic,
			"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"OK\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n",
			`{"content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn"}`,
		},
		{
			core.Gemini,
			"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"OK\"}]},\"finishReason\":\"STOP\"}]}\n\n",
			`{"candidates":[{"content":{"parts":[{"text":"OK"}]},"finishReason":"STOP"}]}`,
		},
		{
			core.OpenAIResponses,
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n",
			`{"output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}],"status":"completed"}`,
		},
	} {
		t.Run(string(tt.api), func(t *testing.T) {
			var bodies []map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				bodies = append(bodies, body)
				if len(bodies) == 1 {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte(tt.stream))
				} else {
					_, _ = w.Write([]byte(tt.compact))
				}
			}))
			defer srv.Close()
			cfg := core.ModelConfig{
				Name: "custom", API: tt.api, BaseURL: srv.URL,
				RequestParameters: core.RequestParameters{
					"vendor": map[string]any{"remove": nil, "keep": false}, "temperature": 0,
				},
			}
			if tt.api == core.OpenAI {
				cfg.Think = core.ThinkConfig{Enabled: true, Mode: core.High}
				cfg.RequestParameters["reasoning_effort"] = nil
				cfg.RequestParameters["enable_thinking"] = false
			}
			c := NewClient(cfg, Hooks{}, nil, "")
			for _, err := range c.Stream(t.Context(), []core.Message{{Role: core.RoleUser, Content: "hi"}}) {
				require.NoError(t, err)
			}
			result, err := c.Compact(t.Context(), llm.CompactRequest{Prompt: "summarize", MaxTokens: 64})
			require.NoError(t, err)
			assert.Equal(t, "OK", result.Text)
			require.Len(t, bodies, 2)
			for _, body := range bodies {
				assert.Equal(t, float64(0), body["temperature"])
				assert.Equal(t, map[string]any{"keep": false}, body["vendor"])
				if tt.api == core.OpenAI {
					assert.Equal(t, "custom", body["model"])
					assert.Equal(t, false, body["enable_thinking"])
					assert.NotContains(t, body, "reasoning_effort")
				}
			}
			switch tt.api {
			case core.Anthropic, core.OpenAI:
				assert.Equal(t, float64(64), bodies[1]["max_tokens"])
			case core.Gemini:
				assert.Equal(t, float64(64), bodies[1]["generationConfig"].(map[string]any)["maxOutputTokens"])
			case core.OpenAIResponses:
				assert.Equal(t, float64(64), bodies[1]["max_output_tokens"])
			}
		})
	}
}

func TestAnthropicOverrideCannotExceedFinalOutputCap(t *testing.T) {
	c := NewClient(core.ModelConfig{
		API: core.Anthropic,
		RequestParameters: core.RequestParameters{
			"thinking": map[string]any{"type": "enabled", "budget_tokens": 30000},
		},
	}, Hooks{}, nil, "")
	c.httpClient = nil
	for _, err := range c.Stream(t.Context(), nil) {
		require.ErrorContains(t, err, "must be less than max_tokens")
	}
	_, err := c.Compact(t.Context(), llm.CompactRequest{Prompt: "sum", MaxTokens: 64})
	require.ErrorContains(t, err, "must be less than max_tokens")
}
