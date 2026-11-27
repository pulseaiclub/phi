package openai

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/core"
)

func TestBuildRequestImages(t *testing.T) {
	cfg := core.ModelConfig{Name: "gpt-4o", APIKey: "k", BaseURL: "https://api.openai.com/v1"}
	req := BuildRequest(cfg, "sys", []core.Message{
		{
			Role:    core.RoleUser,
			Content: "what is this?",
			Images: []core.Image{
				{Data: "QUJD", MimeType: "image/png"},
			},
		},
	}, nil)

	require.Len(t, req.Messages, 2) // system + user
	parts, ok := req.Messages[1].Content.([]any)
	require.True(t, ok, "expected content parts array with images, got %T", req.Messages[1].Content)
	require.Len(t, parts, 2)

	text := parts[0].(map[string]string)
	assert.Equal(t, "text", text["type"])
	assert.Equal(t, "what is this?", text["text"])

	img := parts[1].(map[string]any)
	assert.Equal(t, "image_url", img["type"])
	imageURL := img["image_url"].(map[string]string)
	assert.Equal(t, "data:image/png;base64,QUJD", imageURL["url"])

	body, err := json.Marshal(req)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"image_url"`)
	assert.NotContains(t, string(body), `"images"`)
}

func TestBuildRequestNilToolParameters(t *testing.T) {
	req := BuildRequest(core.ModelConfig{Name: "gpt-4o"}, "", nil, []core.ToolDefinition{{Name: "read"}})
	body, err := json.Marshal(req)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"parameters":null`)
}

func TestBuildRequestNoImagesKeepsStringContent(t *testing.T) {
	cfg := core.ModelConfig{Name: "gpt-4o", APIKey: "k", BaseURL: "https://api.openai.com/v1"}
	req := BuildRequest(cfg, "", []core.Message{{Role: core.RoleUser, Content: "hi"}}, nil)

	body, err := json.Marshal(req)
	require.NoError(t, err)
	// content must stay a JSON string, not an array, when there are no images.
	var raw struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(body, &raw))
	require.Len(t, raw.Messages, 1)
	assert.Equal(t, `"hi"`, string(raw.Messages[0].Content))
}

// prompt_tokens is the whole prompt, so the parser splits cache reads and
// writes out of it. Vendors disagree on which field carries the hits.
func TestNormalizeUsageSplitsCacheBuckets(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		wire usageWire
		want core.Usage
	}{
		"openai details": {
			wire: usageWire{
				PromptTokens: 10, CompletionTokens: 5,
				PromptTokensDetails: &usageWireDetails{CachedTokens: 4},
			},
			// 10 - 4 cached, plus the 5-token reply.
			want: core.Usage{
				PromptTokens: 6, CompletionTokens: 5, TotalTokens: 15,
				PromptTokensDetails: &core.PromptTokensDetails{CachedTokens: 4},
			},
		},
		"deepseek hit field": {
			wire: usageWire{PromptTokens: 100, CompletionTokens: 20, PromptCacheHitTokens: 80},
			want: core.Usage{
				PromptTokens: 20, CompletionTokens: 20, TotalTokens: 120,
				PromptTokensDetails: &core.PromptTokensDetails{CachedTokens: 80},
			},
		},
		"cache write": {
			wire: usageWire{
				PromptTokens: 100, CompletionTokens: 10,
				PromptTokensDetails: &usageWireDetails{CachedTokens: 30, CacheWriteTokens: 20},
			},
			want: core.Usage{
				PromptTokens: 50, CompletionTokens: 10, TotalTokens: 110,
				PromptTokensDetails: &core.PromptTokensDetails{CachedTokens: 30, CacheWriteTokens: 20},
			},
		},
		"hit field and details agree": {
			wire: usageWire{
				PromptTokens: 100, CompletionTokens: 1, PromptCacheHitTokens: 60,
				PromptTokensDetails: &usageWireDetails{CachedTokens: 60},
			},
			want: core.Usage{
				PromptTokens: 40, CompletionTokens: 1, TotalTokens: 101,
				PromptTokensDetails: &core.PromptTokensDetails{CachedTokens: 60},
			},
		},
		"cache larger than prompt clamps at zero": {
			wire: usageWire{
				PromptTokens: 3, CompletionTokens: 1,
				PromptTokensDetails: &usageWireDetails{CachedTokens: 9},
			},
			want: core.Usage{
				PromptTokens: 0, CompletionTokens: 1, TotalTokens: 10,
				PromptTokensDetails: &core.PromptTokensDetails{CachedTokens: 9},
			},
		},
		"no usage": {wire: usageWire{}, want: core.Usage{}},
	}
	for name, tt := range tests {
		assert.Equal(t, tt.want, normalizeUsage(tt.wire), name)
	}
}

func TestBuildRequestDoesNotInferDeepSeekThinking(t *testing.T) {
	cfg := core.ModelConfig{Name: "deepseek-flash", Think: core.ThinkConfig{Enabled: true, Mode: core.High}}
	req := BuildRequest(cfg, "", []core.Message{{Role: core.RoleUser, Content: "hi"}}, nil)
	assert.Nil(t, req.Thinking)
	assert.Equal(t, "high", req.ReasoningEffort)
}

func TestBuildRequestDropsThoughtSignature(t *testing.T) {
	cfg := core.ModelConfig{Name: "gpt-4o", APIKey: "k", BaseURL: "https://api.openai.com/v1"}
	calls := []core.ToolCall{{
		ID: "read", Type: "function", ThoughtSignature: "sig-a",
		Function: core.Function{Name: "read", Arguments: `{"path":"a.go"}`},
	}}
	req := BuildRequest(cfg, "", []core.Message{
		{Role: core.RoleUser, Content: "hi"},
		{Role: core.RoleAssistant, ToolCalls: calls},
	}, nil)

	body, err := json.Marshal(req)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "thought_signature")
	assert.Contains(t, string(body), `"name":"read"`)
	assert.Equal(t, "sig-a", calls[0].ThoughtSignature, "the session's copy keeps its signature")
}
