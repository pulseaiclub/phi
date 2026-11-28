package model

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/core"
	"github.com/pulseaiclub/phi/internal/llm"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
	"github.com/pulseaiclub/phi/internal/llm/openai"
	"github.com/pulseaiclub/phi/internal/util"
)

type parameterTransport func(*http.Request) (*http.Response, error)

func (f parameterTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestParameterAdaptationWire(t *testing.T) {
	tests := []struct {
		name, model, endpoint, thinking string
		overrides                       core.RequestParameters
	}{
		{
			"glm alias native",
			"vendor/glm-5.3-flash",
			"https://api.z.ai/api/coding/paas/v4",
			`{"type":"enabled","clear_thinking":false}`,
			nil,
		},
		{
			"strict endpoint full URL", "vendor/glm-5.3-flash:free",
			"https://opencode.ai/zen/go/v1/chat/completions", "", nil,
		},
		{"glm group alias", "group/auto-glm-5-3-flash", "http://127.0.0.1:8317/v1", `{"type":"enabled"}`, nil},
		{"deepseek at glm endpoint", "deepseek-flash", "https://api.z.ai/api/paas/v4", `{"type":"enabled"}`, nil},
		{
			"deepseek strict endpoint",
			"vendor/deepseek-v4.1-flash",
			"https://opencode.ai/zen/go/v1/chat/completions",
			"",
			nil,
		},
		{
			"override native default",
			"glm-5.3",
			"https://api.z.ai/api/paas/v4",
			`{"type":"enabled","clear_thinking":true}`,
			core.RequestParameters{"thinking": map[string]any{"clear_thinking": true}},
		},
	}
	// Intercept HTTP so native endpoints receive no test traffic.
	httpClient := util.DefaultHTTPClient()
	original := httpClient.Transport
	t.Cleanup(func() { httpClient.Transport = original })
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preset, ok := Lookup(tt.model)
			require.True(t, ok)
			cfg := preset.Config
			cfg.BaseURL, cfg.APIKey, cfg.RequestParameters = tt.endpoint, "fake-key", tt.overrides
			before, err := json.Marshal(cfg.RequestParameters)
			require.NoError(t, err)
			calls := 0
			httpClient.Transport = parameterTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				endpoint := tt.endpoint
				if !strings.HasSuffix(endpoint, "/chat/completions") {
					endpoint += "/chat/completions"
				}
				assert.Equal(t, endpoint, r.URL.String())
				assert.Equal(t, "Bearer fake-key", r.Header.Get("Authorization"))
				var body map[string]json.RawMessage
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				assert.NotContains(t, body, "extra_body")
				if tt.thinking == "" {
					assert.NotContains(t, body, "thinking")
				} else {
					assert.JSONEq(t, tt.thinking, string(body["thinking"]))
				}
				assert.JSONEq(t, `"`+string(cfg.Think.Mode)+`"`, string(body["reasoning_effort"]))
				assert.JSONEq(t, `"`+tt.model+`"`, string(body["model"]))
				response := `{"choices":[{"message":{"content":"summary"},"finish_reason":"stop"}]}`
				if string(body["stream"]) == "true" {
					assert.Contains(t, string(body["messages"]), `"reasoning_content":"prior reasoning"`)
					response = "data: [DONE]\n\n"
				} else {
					assert.Equal(t, "256", string(body["max_tokens"]))
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(response)),
				}, nil
			})
			client := llmclient.NewClient(cfg, HooksFor(cfg.Name), nil, "")
			for _, streamErr := range client.Stream(t.Context(), []core.Message{
				{Role: core.RoleAssistant, Content: "prior answer", ReasoningContent: "prior reasoning"},
				{Role: core.RoleUser, Content: "continue"},
			}) {
				require.NoError(t, streamErr)
			}
			result, err := client.Compact(t.Context(), llm.CompactRequest{Prompt: "summarize", MaxTokens: 256})
			require.NoError(t, err)
			assert.Equal(t, "summary", result.Text)
			assert.Equal(t, 2, calls)
			after, err := json.Marshal(cfg.RequestParameters)
			require.NoError(t, err)
			assert.Equal(t, string(before), string(after), "request processing must not mutate shared configuration")
		})
	}
}

func TestEndpointRuleBoundaries(t *testing.T) {
	for _, endpoint := range []string{
		"https://api.z.ai/api/paas/v4", "HTTPS://API.Z.AI:443/api/paas/v4/",
		"https://api.z.ai/api/coding/paas/v4", "https://open.bigmodel.cn/api/coding/paas/v4/",
		"https://api.z.ai/api/paas/v4/chat/completions",
		"https://open.bigmodel.cn/api/coding/paas/v4/chat/completions",
	} {
		t.Run(endpoint, func(t *testing.T) {
			req := &openai.Request{}
			hook := chatParameters{forced: true}
			require.NoError(t, hook.Before(t.Context(), req, core.ModelConfig{Name: "glm-5.3", BaseURL: endpoint}))
			require.NotNil(t, req.Thinking.ClearThinking)
			assert.False(t, *req.Thinking.ClearThinking)
		})
	}
	for _, endpoint := range []string{
		"http://api.z.ai/api/paas/v4", "https://api.z.ai:8443/api/paas/v4",
		"https://api.z.ai.evil.test/api/paas/v4", "https://gateway.test/api.z.ai/api/paas/v4",
		"https://api.z.ai/api/paas/v40", "https://api.z.ai/API/paas/v4",
		"https://user@api.z.ai/api/paas/v4", "https://api.z.ai/api/paas/v4?route=other",
		"https://api.z.ai/api/paas/v4#other", "://invalid",
		"https://api.z.ai/api/paas/v4/chat/completions?route=other",
		"https://api.z.ai/api/paas/v4/chat/completions#other",
		"https://api.z.ai/api/paas/v4/other/chat/completions",
		"https://api.z.ai/api/paas/v4//chat/completions",
	} {
		t.Run(endpoint, func(t *testing.T) {
			req := &openai.Request{}
			hook := chatParameters{forced: true}
			require.NoError(t, hook.Before(t.Context(), req, core.ModelConfig{Name: "glm-5.3", BaseURL: endpoint}))
			require.NotNil(t, req.Thinking)
			assert.Nil(t, req.Thinking.ClearThinking)
		})
	}
}

func TestModelMappingUsesExplicitAPIAndPresetBinding(t *testing.T) {
	httpClient := util.DefaultHTTPClient()
	original := httpClient.Transport
	t.Cleanup(func() { httpClient.Transport = original })
	for _, tc := range []struct {
		preset             string
		api                core.RouterType
		endpoint, thinking string
	}{
		{"glm-5.3-flash", core.Anthropic, "https://gateway.test/v1", "enabled"},
		{"deepseek-flash", core.Anthropic, "https://gateway.test/v1", "enabled"},
		{"glm-5.3-flash", core.OpenAIResponses, "https://gateway.test/v1", ""},
		{"glm-5.3-flash", core.Anthropic, "https://openrouter.ai/api", "adaptive"},
		{"deepseek-flash", core.Anthropic, "https://openrouter.ai/api/v1", "adaptive"},
		{"deepseek-flash", core.Anthropic, "https://openrouter.ai/api/v1", "disabled"},
	} {
		presetName, api := tc.preset, tc.api
		t.Run(presetName+"/"+string(api)+"/"+tc.endpoint+"/"+tc.thinking, func(t *testing.T) {
			p, ok := Lookup(presetName)
			require.True(t, ok)
			cfg := p.Config
			cfg.Name, cfg.Preset = "opaque-route", presetName
			cfg.API, cfg.BaseURL = api, tc.endpoint
			if tc.thinking == "disabled" {
				cfg.Think = core.ThinkConfig{Mode: core.Off}
			}
			calls := 0
			httpClient.Transport = parameterTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				var body map[string]json.RawMessage
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				assert.Equal(t, `"opaque-route"`, string(body["model"]))
				assert.NotContains(t, body, "reasoning_effort")
				stream := string(body["stream"]) == "true"
				var response string
				if api == core.Anthropic {
					path := "/v1/messages"
					if strings.HasPrefix(tc.endpoint, "https://openrouter.ai/api") {
						path = "/api/v1/messages"
					}
					assert.Equal(t, path, r.URL.Path)
					assert.JSONEq(t, `{"type":"`+tc.thinking+`"}`, string(body["thinking"]))
					if cfg.Think.Enabled {
						assert.JSONEq(t, `{"effort":"`+string(cfg.Think.Mode)+`"}`, string(body["output_config"]))
					} else {
						assert.NotContains(t, body, "output_config")
					}
					response = `{"content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn"}`
					if stream {
						response = "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"OK\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
					} else {
						assert.Equal(t, "64", string(body["max_tokens"]))
					}
				} else {
					assert.Equal(t, "/v1/responses", r.URL.Path)
					assert.NotContains(t, body, "thinking")
					assert.JSONEq(
						t,
						`{"effort":"`+string(cfg.Think.Mode)+`","summary":"auto"}`,
						string(body["reasoning"]),
					)
					response = `{"output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}],"status":"completed"}`
					if stream {
						response = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
					} else {
						assert.Equal(t, "64", string(body["max_output_tokens"]))
					}
				}
				return &http.Response{
					StatusCode: http.StatusOK, Header: make(http.Header),
					Body: io.NopCloser(strings.NewReader(response)),
				}, nil
			})
			client := llmclient.NewClient(cfg, HooksForConfig(cfg), nil, "")
			for _, err := range client.Stream(t.Context(), []core.Message{{Role: core.RoleUser, Content: "hi"}}) {
				require.NoError(t, err)
			}
			result, err := client.Compact(t.Context(), llm.CompactRequest{Prompt: "sum", MaxTokens: 64})
			require.NoError(t, err)
			assert.Equal(t, "OK", result.Text)
			assert.Equal(t, 2, calls)
		})
	}
}

func TestDeepSeekChatThinkingDisabled(t *testing.T) {
	p, ok := Lookup("vendor/deepseek-flash")
	require.True(t, ok)
	cfg := p.Config
	cfg.Think = core.ThinkConfig{Mode: core.Off}
	req := openai.BuildRequest(cfg, "", nil, nil)
	require.NoError(t, p.Hooks.OpenAI.Before(t.Context(), req, cfg))
	assert.Equal(t, "disabled", req.Thinking.Type)
	assert.Empty(t, req.ReasoningEffort)
}

func TestForcedAnthropicThinkingKeepsSignedReplayWhenThinkDisabled(t *testing.T) {
	httpClient := util.DefaultHTTPClient()
	original := httpClient.Transport
	t.Cleanup(func() { httpClient.Transport = original })

	sse := strings.Join([]string{
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"plan","signature":"sig-1"}}`,
		`data: {"type":"content_block_stop","index":0}`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"read","input":{}}}`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.go\"}"}}`,
		`data: {"type":"content_block_stop","index":1}`,
		`data: {"type":"message_stop"}`,
	}, "\n\n") + "\n\n"

	for _, tc := range []struct {
		name         string
		baseURL      string
		wantThinking string
		wantEffort   string
	}{
		{
			name:         "claude-opus-5-5",
			baseURL:      "https://api.anthropic.com",
			wantThinking: `"thinking":{"type":"adaptive"}`,
			wantEffort:   `"output_config":{"effort":"low"}`,
		},
		{
			name:         "glm-5.3-flash",
			baseURL:      "https://gateway.test/v1",
			wantThinking: `"thinking":{"type":"enabled"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var bodies []string
			httpClient.Transport = parameterTransport(func(r *http.Request) (*http.Response, error) {
				raw, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				bodies = append(bodies, string(raw))
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(sse)),
				}, nil
			})

			cfg := core.ModelConfig{
				Name:    tc.name,
				BaseURL: tc.baseURL,
				APIKey:  "sk-ant",
				API:     core.Anthropic,
				Think:   core.ThinkConfig{Enabled: false, Mode: core.Off},
			}
			tools := []core.ToolDefinition{{Name: "read", Description: "read file"}}
			client := llmclient.NewClient(cfg, HooksForConfig(cfg), tools, "sys")

			user := core.Message{Role: core.RoleUser, Content: "inspect"}
			var assistant *core.Message
			for ev, err := range client.Stream(t.Context(), []core.Message{user}) {
				require.NoError(t, err)
				if ev.Final != nil {
					assistant = ev.Final
				}
			}
			require.NotNil(t, assistant)
			require.NotNil(t, assistant.ProviderState, "turn 1 must capture signed thinking blocks")

			toolResult := core.Message{Role: core.RoleTool, ToolCallID: "toolu_1", Content: "package a"}
			for _, err := range client.Stream(t.Context(), []core.Message{user, *assistant, toolResult}) {
				require.NoError(t, err)
			}
			require.Len(t, bodies, 2)
			assert.Contains(t, bodies[1], tc.wantThinking)
			if tc.wantEffort != "" {
				assert.Contains(t, bodies[1], tc.wantEffort)
			} else {
				assert.NotContains(t, bodies[1], `"output_config"`)
			}
			assert.Contains(t, bodies[1], `"signature":"sig-1"`, "forced thinking must replay signed blocks")
		})
	}
}
