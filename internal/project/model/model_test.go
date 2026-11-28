package model

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/llm/anthropic"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
	"github.com/pulseaiclub/phi/internal/llm/gemini"
	"github.com/pulseaiclub/phi/internal/llm/openai"
	"github.com/pulseaiclub/phi/internal/llm/openai/responses"
)

func TestLookupGPTPresets(t *testing.T) {
	tests := []struct {
		name          string
		contextWindow int
		inputLimit    int
		maxTokens     int
		thinking      llm.ThinkConfig
		modes         []llm.ThinkMode
		noneOff       bool
	}{
		{
			name:          "gpt-6.2-astra",
			contextWindow: 1_050_000,
			inputLimit:    922_000,
			maxTokens:     128_000,
			thinking:      llm.ThinkConfig{Enabled: true, Mode: llm.Max},
			modes:         lowToMaxModes,
		},
		{
			name:          "gpt-6.2-sol",
			contextWindow: 1_050_000,
			inputLimit:    922_000,
			maxTokens:     128_000,
			thinking:      llm.ThinkConfig{Enabled: true, Mode: llm.High},
			modes:         lowToMaxModes,
		},
		{
			name:          "gpt-6.1-sol",
			contextWindow: 1_050_000,
			inputLimit:    922_000,
			maxTokens:     128_000,
			thinking:      llm.ThinkConfig{Enabled: true, Mode: llm.High},
			modes:         lowToMaxModes,
		},
		{
			name:          "gpt-6.1-terra",
			contextWindow: 1_050_000,
			inputLimit:    922_000,
			maxTokens:     128_000,
			thinking:      llm.ThinkConfig{Enabled: true, Mode: llm.High},
			modes:         offLowToMaxModes,
			noneOff:       true,
		},
		{
			name:          "gpt-6-astra",
			contextWindow: 1_050_000,
			inputLimit:    922_000,
			maxTokens:     128_000,
			thinking:      llm.ThinkConfig{Enabled: true, Mode: llm.Max},
		},
		{
			name:          "gpt-6-luna",
			contextWindow: 1_050_000,
			inputLimit:    922_000,
			maxTokens:     128_000,
			thinking:      llm.ThinkConfig{Enabled: true, Mode: llm.High},
			modes:         offLowToMaxModes,
			noneOff:       true,
		},
		{
			name:          "gpt-5.6-sol",
			contextWindow: 1_050_000,
			inputLimit:    922_000,
			maxTokens:     128_000,
			thinking:      llm.ThinkConfig{Enabled: true, Mode: llm.High},
		},
		{
			name:          "gpt-5.6-terra",
			contextWindow: 1_050_000,
			inputLimit:    922_000,
			maxTokens:     128_000,
			thinking:      llm.ThinkConfig{Enabled: true, Mode: llm.High},
		},
		{
			name:          "gpt-5.6-luna",
			contextWindow: 1_050_000,
			inputLimit:    922_000,
			maxTokens:     128_000,
			thinking:      llm.ThinkConfig{Enabled: true, Mode: llm.High},
		},
		{name: "gpt-5-chat-latest", contextWindow: 128_000, inputLimit: 0, maxTokens: 0, thinking: llm.ThinkConfig{}},
		{
			name:          "gpt-5.5",
			contextWindow: 1_050_000,
			inputLimit:    922_000,
			maxTokens:     128_000,
			thinking:      llm.ThinkConfig{Enabled: true, Mode: llm.High},
		},
		{
			name:          "gpt-5.5-pro",
			contextWindow: 1_050_000,
			inputLimit:    922_000,
			maxTokens:     128_000,
			thinking:      llm.ThinkConfig{Enabled: true, Mode: llm.High},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, ok := Lookup(tt.name)
			require.True(t, ok)
			assert.Equal(t, "https://api.openai.com/v1", p.Config.BaseURL)
			assert.Equal(t, tt.contextWindow, p.Config.ContextWindow)
			assert.Equal(t, tt.inputLimit, p.Config.InputLimit)
			assert.Equal(t, tt.maxTokens, p.Config.MaxTokens)
			assert.True(t, p.Config.ImageEnabled)
			assert.Equal(t, llm.OpenAIResponses, p.Config.API)
			assert.Equal(t, tt.thinking, p.Config.Think)
			if len(tt.modes) > 0 {
				assert.Equal(t, tt.modes, ThinkingModes(p.Config))
			}
			if tt.noneOff {
				require.NotNil(t, p.Hooks.OpenAIResponses)
				offCfg := p.Config
				offCfg.Think = llm.ThinkConfig{Enabled: false}
				req := responses.BuildRequest(offCfg, "", []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, nil)
				require.NoError(t, p.Hooks.OpenAIResponses.Before(t.Context(), req, offCfg))
				assert.Equal(t, &responses.Reasoning{Effort: "none"}, req.Reasoning)

				onReq := responses.BuildRequest(p.Config, "", []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, nil)
				require.NoError(t, p.Hooks.OpenAIResponses.Before(t.Context(), onReq, p.Config))
				assert.Equal(t, &responses.Reasoning{Effort: "high", Summary: "auto"}, onReq.Reasoning)
			} else {
				assert.Empty(t, p.Hooks)
			}
		})
	}
}

func TestLookupClaudePresets(t *testing.T) {
	tests := []struct {
		name     string
		dotAlias string
		modes    []llm.ThinkMode
		allowOff bool
	}{
		{"claude-fable-5-2", "anthropic/claude-fable-5.2", lowToMaxModes, false},
		{"claude-opus-5-5", "anthropic/claude-opus-5.5", lowToMaxModes, false},
		{"claude-sonnet-5-5", "anthropic/claude-sonnet-5.5", lowToMaxModes, false},
		{"claude-haiku-5-5", "anthropic/claude-haiku-5.5", offLowToMaxModes, true},
		{"claude-opus-4-6", "anthropic/claude-opus-4.6", claude46Modes, true},
		{"claude-sonnet-4-6", "anthropic/claude-sonnet-4.6", claude46Modes, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, ok := Lookup(tt.name)
			require.True(t, ok)
			assert.Equal(t, "https://api.anthropic.com", p.Config.BaseURL)
			assert.Equal(t, llm.Anthropic, p.Config.API)
			assert.Equal(t, thinkHigh, p.Config.Think)
			assert.Equal(t, tt.modes, ThinkingModes(p.Config))
			require.NotNil(t, p.Hooks.Anthropic)

			offCfg := p.Config
			offCfg.Think = llm.ThinkConfig{Enabled: false}
			offReq := anthropic.BuildRequest(offCfg, "", []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, nil)
			require.NoError(t, p.Hooks.Anthropic.Before(t.Context(), &offReq, offCfg))
			body, err := json.Marshal(offReq)
			require.NoError(t, err)
			if tt.allowOff {
				assert.Contains(t, string(body), `"thinking":{"type":"disabled"}`)
				assert.NotContains(t, string(body), `"output_config"`)
			} else {
				assert.Contains(t, string(body), `"thinking":{"type":"adaptive"}`)
				assert.Contains(t, string(body), `"output_config":{"effort":"low"}`)
			}

			routed, ok := Lookup(tt.dotAlias)
			require.True(t, ok, tt.dotAlias)
			assert.Empty(t, routed.Config.BaseURL)
			assert.Equal(t, 1_000_000, routed.Config.ContextWindow)
			assert.NotNil(t, routed.Hooks.Anthropic)
		})
	}
}

func TestClaudePresetSignedToolContinuation(t *testing.T) {
	const signedTurn = `data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"look up","signature":"sig/+=="}}

data: {"type":"content_block_stop","index":0}

data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu.call","name":"read","input":{}}}

data: {"type":"content_block_stop","index":1}

data: {"type":"message_stop"}

`
	for _, tc := range []struct {
		preset       string
		wantThinking string
		wantID       string
	}{
		{"claude-opus-5-5", "adaptive", "toolu.call"},
		{"claude-haiku-5-5", "disabled", "toolu_call"},
	} {
		t.Run(tc.preset, func(t *testing.T) {
			requests := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				requests <- body
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, signedTurn)
			}))
			defer server.Close()

			cfg := llm.ModelConfig{
				Name: "opaque-route", Preset: tc.preset, API: llm.Anthropic,
				BaseURL: server.URL, Think: llm.ThinkConfig{Enabled: true, Mode: llm.High},
			}
			tools := []llm.ToolDefinition{{Name: "read", Params: &llm.FunctionParameters{Type: "object"}}}
			messages := []llm.Message{{Role: llm.RoleUser, Content: "read a file"}}
			stream := func() (*llm.Message, []byte) {
				t.Helper()
				client := llmclient.NewClient(cfg, HooksForConfig(cfg), tools, "")
				var final *llm.Message
				for event, err := range client.Stream(t.Context(), messages) {
					require.NoError(t, err)
					if event.Final != nil {
						final = event.Final
					}
				}
				require.NotNil(t, final)
				return final, <-requests
			}
			assistant, _ := stream()
			require.NotNil(t, assistant.ProviderState)
			require.Len(t, assistant.ToolCalls, 1)
			messages = append(messages, *assistant, llm.Message{
				Role: llm.RoleTool, ToolCallID: assistant.ToolCalls[0].ID, Content: "file contents",
			})
			cfg.Think = llm.ThinkConfig{Enabled: false, Mode: llm.Off}
			_, body := stream()
			var wire struct {
				Thinking     json.RawMessage `json:"thinking"`
				OutputConfig json.RawMessage `json:"output_config"`
				Messages     []struct{ Content json.RawMessage }
			}
			require.NoError(t, json.Unmarshal(body, &wire))
			require.Len(t, wire.Messages, 3)
			assert.JSONEq(t, `{"type":"`+tc.wantThinking+`"}`, string(wire.Thinking))
			blocks := `{"type":"tool_use","id":"` + tc.wantID + `","name":"read","input":{}}`
			if tc.wantThinking == "adaptive" {
				assert.JSONEq(t, `{"effort":"low"}`, string(wire.OutputConfig))
				blocks = `{"type":"thinking","thinking":"look up","signature":"sig/+=="},` + blocks
			}
			assert.JSONEq(t, "["+blocks+"]", string(wire.Messages[1].Content))
			assert.Contains(t, string(wire.Messages[2].Content), `"tool_use_id":"`+tc.wantID+`"`)
		})
	}
}

func TestLookupDeepSeekFlash(t *testing.T) {
	p, ok := Lookup("deepseek-flash")
	require.True(t, ok)
	assert.Equal(t, "deepseek-flash", p.Config.Name)
	assert.Equal(t, "https://api.deepseek.com", p.Config.BaseURL)
	assert.Equal(t, 1_048_576, p.Config.ContextWindow)
	assert.Equal(t, 393_216, p.Config.MaxTokens)
	assert.True(t, p.Config.ImageEnabled, "V4.1-Flash accepts image input")
	assert.NotNil(t, p.Hooks.OpenAI)
}

func TestLookupDeepSeekV4Pro(t *testing.T) {
	p, ok := Lookup("deepseek-v4-pro")
	require.True(t, ok)
	assert.Equal(t, "deepseek-v4-pro", p.Config.Name)
	assert.Equal(t, "https://api.deepseek.com", p.Config.BaseURL)
	assert.Equal(t, 1_048_576, p.Config.ContextWindow)
	assert.Equal(t, 393_216, p.Config.MaxTokens)
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
		ContextWindow: 1_048_576,
		MaxTokens:     393_216,
		ImageEnabled:  true,
		API:           llm.OpenAI,
		Think:         llm.ThinkConfig{Enabled: true, Mode: llm.High},
	}, p.Config)

	// A gateway route inherits the output limit with the rest of the preset.
	routed, ok := Lookup("deepseek/deepseek-flash")
	require.True(t, ok)
	assert.Equal(t, 393_216, routed.Config.MaxTokens)
}

func TestDeepSeekInterceptorSetsTopLevelThinking(t *testing.T) {
	req := &openai.Request{Model: "deepseek-flash"}
	require.NoError(t, chatParameters{}.Before(t.Context(), req, llm.ModelConfig{
		Name: "deepseek-flash", Think: llm.ThinkConfig{Enabled: true, Mode: llm.High},
	}))
	require.NotNil(t, req.Thinking)
	assert.Equal(t, "enabled", req.Thinking.Type)

	body, err := json.Marshal(req)
	require.NoError(t, err)
	assert.NotContains(t, string(body), `"extra_body"`)
	assert.Contains(t, string(body), `"thinking"`)
}

func TestLookupGLM53(t *testing.T) {
	p, ok := Lookup("glm-5.3")
	require.True(t, ok)
	assert.Equal(t, "https://api.z.ai/api/coding/paas/v4", p.Config.BaseURL)
	assert.Equal(t, 1_000_000, p.Config.ContextWindow)
	assert.Equal(t, 131_072, p.Config.MaxTokens)
	assert.False(t, p.Config.ImageEnabled, "5.3 is text-only")
}

func TestLookupGLM53Flash(t *testing.T) {
	p, ok := Lookup("glm-5.3-flash")
	require.True(t, ok)
	assert.Equal(t, "https://api.z.ai/api/coding/paas/v4", p.Config.BaseURL)
	assert.Equal(t, 1_000_000, p.Config.ContextWindow)
	assert.Equal(t, 131_072, p.Config.MaxTokens)
	assert.True(t, p.Config.ImageEnabled, "5.3-flash is natively multimodal")
	assert.Equal(t, llm.OpenAI, p.Config.API)
}

func TestGLMRetiredNamesAreNotPresets(t *testing.T) {
	for _, name := range []string{"glm-5-turbo", "glm-5.1", "glm-5v-turbo", "glm-4.5-air", "glm-4.7"} {
		_, ok := Lookup(name)
		assert.False(t, ok, "%s is no longer a preset", name)
	}
}

// All GLM presets are forced-thinking: thinking.type is always enabled and
// clear_thinking is false (Preserved Thinking) so reasoning survives across
// turns. reasoning_effort rides along as the depth of the thought chain.
func TestGLMPresetWiresThinkingForNativeEndpoint(t *testing.T) {
	for _, name := range []string{"glm-5.4", "glm-5.3", "glm-5.3-flash"} {
		p, ok := Lookup(name)
		require.True(t, ok, name)
		require.NotNil(t, p.Hooks.OpenAI, name)

		req := openai.BuildRequest(p.Config, "", nil, nil)
		require.NoError(t, p.Hooks.OpenAI.Before(t.Context(), req, p.Config))
		require.NotNil(t, req.Thinking, name)
		assert.Equal(t, "enabled", req.Thinking.Type, name)
		require.NotNil(t, req.Thinking.ClearThinking, name)
		assert.False(t, *req.Thinking.ClearThinking, "%s preserves reasoning", name)
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
	for _, name := range []string{"glm-5.4", "glm-5.3", "glm-5.3-flash"} {
		p, ok := Lookup(name)
		require.True(t, ok, name)
		cfg := p.Config
		cfg.Think = llm.ThinkConfig{Enabled: false}

		req := openai.BuildRequest(cfg, "", nil, nil)
		require.NoError(t, p.Hooks.OpenAI.Before(t.Context(), req, cfg))
		require.NotNil(t, req.Thinking, name)
		assert.Equal(t, "enabled", req.Thinking.Type, name)
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
	body, err := json.Marshal(req.GenerationConfig.ThinkingConfig)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"thinkingBudget":2048`)
}

func TestGeminiLevelHookOffFloor(t *testing.T) {
	p, ok := Lookup("gemini-3-pro")
	require.True(t, ok)
	require.NotNil(t, p.Hooks.Gemini)

	req := &gemini.GeminiRequest{}
	require.NoError(t, p.Hooks.Gemini.Before(t.Context(), req, llm.ModelConfig{}))
	body, err := json.Marshal(req.GenerationConfig.ThinkingConfig)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"thinkingLevel":"LOW"`)
}

func TestLookupGemini4And38Presets(t *testing.T) {
	for _, tt := range []struct {
		name        string
		defaultMode llm.ThinkMode
		modes       []llm.ThinkMode
		xhighWire   string
	}{
		{"gemini-4-carbon", llm.Medium, geminiLowToHighModes, `"thinkingLevel":"HIGH"`},
		{"gemini-4-argon", llm.Medium, geminiLowToHighModes, `"thinkingLevel":"HIGH"`},
		{"gemini-4-neon", llm.High, gemini4XHighModes, `"thinkingLevel":"XHIGH"`},
		{"gemini-4-hydrogen", llm.High, gemini4XHighModes, `"thinkingLevel":"XHIGH"`},
		{"gemini-3.8-flash", llm.High, geminiLowToHighModes, `"thinkingLevel":"HIGH"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, ok := Lookup(tt.name)
			require.True(t, ok)
			assert.Equal(t, tt.defaultMode, p.Config.Think.Mode)
			assert.Equal(t, tt.modes, ThinkingModes(p.Config))
			require.NotNil(t, p.Hooks.Gemini)

			offReq := &gemini.GeminiRequest{}
			require.NoError(t, p.Hooks.Gemini.Before(t.Context(), offReq, llm.ModelConfig{}))
			offBody, err := json.Marshal(offReq.GenerationConfig.ThinkingConfig)
			require.NoError(t, err)
			assert.Contains(t, string(offBody), `"thinkingLevel":"LOW"`)

			xhighReq := &gemini.GeminiRequest{}
			require.NoError(t, p.Hooks.Gemini.Before(t.Context(), xhighReq, llm.ModelConfig{
				Think: llm.ThinkConfig{Enabled: true, Mode: llm.XHigh},
			}))
			xhighBody, err := json.Marshal(xhighReq.GenerationConfig.ThinkingConfig)
			require.NoError(t, err)
			assert.Contains(t, string(xhighBody), tt.xhighWire)
		})
	}
}

func TestSyncedSpecsMatchSources(t *testing.T) {
	raw, err := os.ReadFile("sources.json")
	require.NoError(t, err)

	var sources map[string]string
	require.NoError(t, json.Unmarshal(raw, &sources))
	require.Len(t, syncedSpecs, len(sources))

	maxTokenOverrides := map[string]int{
		"kimi-k3":       131_072,
		"kimi-k3.1-pro": 131_072,
	}

	byName := make(map[string]Preset, len(presets))
	for _, p := range presets {
		byName[p.Config.Name] = p
		assert.Positive(t, p.Config.ContextWindow, p.Config.Name)
	}
	for name := range sources {
		p, ok := byName[name]
		require.True(t, ok, "source %q must match a built-in preset", name)
		spec, ok := syncedSpecs[name]
		require.True(t, ok, "source %q must exist in syncedSpecs", name)
		assert.Equal(t, spec.ContextWindow, p.Config.ContextWindow, name)
		assert.Equal(t, spec.InputLimit, p.Config.InputLimit, name)
		assert.Equal(t, spec.ImageEnabled, p.Config.ImageEnabled, name)
		wantMax := spec.MaxTokens
		if override, has := maxTokenOverrides[name]; has {
			wantMax = override
		}
		assert.Equal(t, wantMax, p.Config.MaxTokens, name)
	}
}

func TestSyncedSpecPreservesExplicitOverrides(t *testing.T) {
	k3, ok := Lookup("kimi-k3")
	require.True(t, ok)
	assert.Equal(t, 1_048_576, k3.Config.ContextWindow)
	assert.Equal(t, 131_072, k3.Config.MaxTokens)
	assert.True(t, k3.Config.ImageEnabled)

	custom := withSyncedSpec(llm.ModelConfig{Name: "gpt-5.5", ContextWindow: 2_000_000})
	assert.Equal(t, 2_000_000, custom.ContextWindow)
	assert.Zero(t, custom.InputLimit, "overriding ContextWindow skips the synced InputLimit")
	assert.Equal(t, 2_000_000, custom.CompactionWindow())
	assert.Equal(t, 128_000, custom.MaxTokens)

	// Coding-plan 10M tier stays hand-maintained so it does not inherit the
	// standard tier's 262K output limit for compaction summaries.
	assert.NotContains(t, syncedSpecs, "kimi-k2.7-code")
	k27, ok := Lookup("kimi-k2.7-code")
	require.True(t, ok)
	assert.Equal(t, 10_000_000, k27.Config.ContextWindow)
	assert.Zero(t, k27.Config.MaxTokens)
	assert.True(t, k27.Config.ImageEnabled)
}
