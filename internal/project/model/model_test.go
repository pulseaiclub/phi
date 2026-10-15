package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/llm/gemini"
	"github.com/pulseaiclub/phi/internal/llm/openai"
)

func TestLookupGPTPresets(t *testing.T) {
	tests := []struct {
		name          string
		contextWindow int
		thinking      llm.ThinkConfig
	}{
		{name: "gpt-6-astra", contextWindow: 272_000, thinking: llm.ThinkConfig{Enabled: true, Mode: llm.Max}},
		{name: "gpt-5.6-sol", contextWindow: 272_000, thinking: llm.ThinkConfig{Enabled: true, Mode: llm.High}},
		{name: "gpt-5.6-terra", contextWindow: 272_000, thinking: llm.ThinkConfig{Enabled: true, Mode: llm.High}},
		{name: "gpt-5.6-luna", contextWindow: 272_000, thinking: llm.ThinkConfig{Enabled: true, Mode: llm.High}},
		{name: "gpt-5-chat-latest", contextWindow: 128_000, thinking: llm.ThinkConfig{}},
		{name: "gpt-5.5", contextWindow: 272_000, thinking: llm.ThinkConfig{Enabled: true, Mode: llm.High}},
		{name: "gpt-5.5-pro", contextWindow: 1_050_000, thinking: llm.ThinkConfig{Enabled: true, Mode: llm.High}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, ok := Lookup(tt.name)
			require.True(t, ok)
			assert.Equal(t, "https://api.openai.com/v1", p.Config.BaseURL)
			assert.Equal(t, tt.contextWindow, p.Config.ContextWindow)
			assert.True(t, p.Config.ImageEnabled)
			assert.Equal(t, llm.OpenAIResponses, p.Config.API)
			assert.Equal(t, tt.thinking, p.Config.Think)
			assert.Empty(t, p.Hooks)
		})
	}
}

func TestLookupDeepSeekFlash(t *testing.T) {
	p, ok := Lookup("deepseek-flash")
	require.True(t, ok)
	assert.Equal(t, "deepseek-flash", p.Config.Name)
	assert.Equal(t, "https://api.deepseek.com", p.Config.BaseURL)
	assert.Equal(t, 1_000_000, p.Config.ContextWindow)
	assert.True(t, p.Config.ImageEnabled, "V4.1-Flash accepts image input")
	assert.NotNil(t, p.Hooks.OpenAI)
}

func TestLookupDeepSeekV4Pro(t *testing.T) {
	p, ok := Lookup("deepseek-v4-pro")
	require.True(t, ok)
	assert.Equal(t, "deepseek-v4-pro", p.Config.Name)
	assert.Equal(t, "https://api.deepseek.com", p.Config.BaseURL)
	assert.Equal(t, 1_000_000, p.Config.ContextWindow)
	assert.False(t, p.Config.ImageEnabled, "V4-Pro has no image understanding")
	assert.NotNil(t, p.Hooks.OpenAI)
}

func TestLookupUnknownFallsThrough(t *testing.T) {
	// Legacy / custom names are not presets; callers apply the generic
	// OpenAI default themselves.
	_, ok := Lookup("deepseek-chat")
	assert.False(t, ok)
	_, ok = Lookup("some-unknown-model")
	assert.False(t, ok)

	// A preset never leaks api_key or skill path — those stay caller-owned.
	p, ok := Lookup("deepseek-flash")
	require.True(t, ok)
	assert.Empty(t, p.Config.APIKey)
	assert.Empty(t, p.Config.SkillPaths)
	assert.Equal(t, llm.ModelConfig{
		Name:          "deepseek-flash",
		BaseURL:       "https://api.deepseek.com",
		ContextWindow: 1_000_000,
		ImageEnabled:  true,
		API:           llm.OpenAI,
		Think:         llm.ThinkConfig{Enabled: true, Mode: llm.High},
	}, p.Config)
}

func TestDeepSeekInterceptorSetsExtraBody(t *testing.T) {
	req := &openai.Request{Model: "deepseek-flash"}
	require.NoError(t, deepseekThinking{}.Before(t.Context(), req, llm.ModelConfig{}))
	require.NotNil(t, req.ExtraBody)
	require.NotNil(t, req.ExtraBody.Thinking)
	assert.Equal(t, "enabled", req.ExtraBody.Thinking.Type)

	body, err := json.Marshal(req)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"extra_body"`)
	assert.Contains(t, string(body), `"thinking"`)
}

func TestLookupGLM53(t *testing.T) {
	p, ok := Lookup("glm-5.3")
	require.True(t, ok)
	assert.Equal(t, "https://api.z.ai/api/coding/paas/v4", p.Config.BaseURL)
	assert.Equal(t, 1_048_576, p.Config.ContextWindow)
	assert.False(t, p.Config.ImageEnabled, "5.3 is text-only")
}

func TestLookupGLM53Flash(t *testing.T) {
	p, ok := Lookup("glm-5.3-flash")
	require.True(t, ok)
	assert.Equal(t, "https://api.z.ai/api/coding/paas/v4", p.Config.BaseURL)
	assert.Equal(t, 1_048_576, p.Config.ContextWindow)
	assert.True(t, p.Config.ImageEnabled, "5.3-flash is natively multimodal")
	assert.Equal(t, llm.OpenAI, p.Config.API)
}

func TestGLMRetiredNamesAreNotPresets(t *testing.T) {
	for _, name := range []string{"glm-5-turbo", "glm-5.1", "glm-5v-turbo", "glm-4.5-air", "glm-4.7"} {
		_, ok := Lookup(name)
		assert.False(t, ok, "%s is no longer a preset", name)
	}
}

// Both GLM presets are forced-thinking: thinking.type is always enabled and
// clear_thinking is false (Preserved Thinking) so reasoning survives across
// turns. reasoning_effort rides along as the depth of the thought chain.
func TestGLMPresetWiresThinkingThroughExtraBody(t *testing.T) {
	for _, name := range []string{"glm-5.3", "glm-5.3-flash"} {
		p, ok := Lookup(name)
		require.True(t, ok, name)
		require.NotNil(t, p.Hooks.OpenAI, name)

		req := openai.BuildRequest(p.Config, "", nil, nil)
		require.NoError(t, p.Hooks.OpenAI.Before(t.Context(), req, p.Config))
		require.NotNil(t, req.ExtraBody, name)
		require.NotNil(t, req.ExtraBody.Thinking, name)
		assert.Equal(t, "enabled", req.ExtraBody.Thinking.Type, name)
		require.NotNil(t, req.ExtraBody.Thinking.ClearThinking, name)
		assert.False(t, *req.ExtraBody.Thinking.ClearThinking, "%s preserves reasoning", name)
		assert.Equal(t, "max", req.ReasoningEffort, name)

		body, err := json.Marshal(req)
		require.NoError(t, err)
		assert.Contains(t, string(body), `"thinking":{"type":"enabled","clear_thinking":false}`, name)
		assert.Contains(t, string(body), `"reasoning_effort":"max"`, name)
	}
}

// z.ai errors on thinking.type=disabled, so turning the session's thinking off
// must not turn it off on the wire either — it only drops the depth knob.
func TestGLMThinkingCannotBeDisabled(t *testing.T) {
	for _, name := range []string{"glm-5.3", "glm-5.3-flash"} {
		p, ok := Lookup(name)
		require.True(t, ok, name)
		cfg := p.Config
		cfg.Think = llm.ThinkConfig{Enabled: false}

		req := openai.BuildRequest(cfg, "", nil, nil)
		require.NoError(t, p.Hooks.OpenAI.Before(t.Context(), req, cfg))
		require.NotNil(t, req.ExtraBody, name)
		require.NotNil(t, req.ExtraBody.Thinking, name)
		assert.Equal(t, "enabled", req.ExtraBody.Thinking.Type, name)
		assert.Empty(t, req.ReasoningEffort, "off drops the depth, not thinking itself")
	}
}

func TestHooksForUnknownIsZero(t *testing.T) {
	h := HooksFor("no-such-model")
	assert.Nil(t, h.OpenAI)
	assert.Nil(t, h.Anthropic)
	assert.Nil(t, h.Gemini)
}

func TestGeminiBudgetHookUsesLiveThink(t *testing.T) {
	p, ok := Lookup("gemini-2.5-flash")
	require.True(t, ok)
	require.NotNil(t, p.Hooks.Gemini)

	req := &gemini.GeminiRequest{}
	cfg := llm.ModelConfig{Think: llm.ThinkConfig{Enabled: true, Mode: llm.Low}}
	require.NoError(t, p.Hooks.Gemini.Before(t.Context(), req, cfg))
	body, err := json.Marshal(req.ThinkingConfig)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"thinkingBudget":2048`)
}

func TestGeminiLevelHookOffFloor(t *testing.T) {
	p, ok := Lookup("gemini-3-pro")
	require.True(t, ok)
	require.NotNil(t, p.Hooks.Gemini)

	req := &gemini.GeminiRequest{}
	require.NoError(t, p.Hooks.Gemini.Before(t.Context(), req, llm.ModelConfig{}))
	body, err := json.Marshal(req.ThinkingConfig)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"thinkingLevel":"LOW"`)
}
