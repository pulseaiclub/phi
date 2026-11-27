package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/tui/configui"
)

func modelIDs(models []configui.ModelInfo) []string {
	ids := make([]string, len(models))
	for i, m := range models {
		ids[i] = m.ID
	}
	return ids
}

func TestFetchModels(t *testing.T) {
	cases := []struct {
		name        string
		api         string
		model       string
		basePath    string
		response    string
		wantPath    string
		wantModels  []string
		checkAuth   func(*testing.T, *http.Request)
		withBaseURL bool
	}{
		{
			name:       "openai compatible",
			api:        "OpenAI",
			model:      "gpt-4o",
			basePath:   "/v1",
			response:   `{"data":[{"id":"z-model"},{"id":"a-model"},{"id":"z-model"}]}`,
			wantPath:   "/v1/models",
			wantModels: []string{"a-model", "z-model"},
			checkAuth: func(t *testing.T, r *http.Request) {
				assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
				assert.Empty(t, r.Header.Get("Anthropic-Version"))
			},
			withBaseURL: true,
		},
		{
			name:       "anthropic explicit api",
			api:        "Anthropic",
			model:      "claude-sonnet-4-20250514",
			basePath:   "/v1",
			response:   `{"data":[{"id":"claude-sonnet-4-20250514","display_name":"Claude Sonnet"}]}`,
			wantPath:   "/v1/models",
			wantModels: []string{"claude-sonnet-4-20250514"},
			checkAuth: func(t *testing.T, r *http.Request) {
				assert.Equal(t, "test-key", r.Header.Get("X-Api-Key"))
				assert.Equal(t, anthropicAPIVersion, r.Header.Get("Anthropic-Version"))
			},
			withBaseURL: true,
		},
		{
			name:       "claude name with openai api stays openai",
			api:        "OpenAI",
			model:      "claude-via-proxy",
			basePath:   "/v1",
			response:   `{"models":[{"name":"proxy-model"}]}`,
			wantPath:   "/v1/models",
			wantModels: []string{"proxy-model"},
			checkAuth: func(t *testing.T, r *http.Request) {
				assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
				assert.Empty(t, r.Header.Get("X-Api-Key"))
			},
			withBaseURL: true,
		},
		{
			name:       "gemini uses x-goog-api-key and strips models prefix",
			api:        "Gemini",
			model:      "gemini-4-neon",
			basePath:   "/v1beta",
			response:   `{"models":[{"name":"models/gemini-4-neon","inputTokenLimit":1048576,"outputTokenLimit":65536}]}`,
			wantPath:   "/v1beta/models",
			wantModels: []string{"gemini-4-neon"},
			checkAuth: func(t *testing.T, r *http.Request) {
				assert.Equal(t, "test-key", r.Header.Get("x-goog-api-key"))
				assert.Empty(t, r.Header.Get("Authorization"))
				assert.Equal(t, "1000", r.URL.Query().Get("pageSize"))
			},
			withBaseURL: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				assert.Equal(t, tc.wantPath, r.URL.Path)
				tc.checkAuth(t, r)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.response))
			}))
			defer server.Close()

			baseURL := ""
			if tc.withBaseURL {
				baseURL = server.URL + tc.basePath
			}
			got, err := fetchModels(t.Context(), baseURL, "test-key", tc.api, tc.model)
			require.NoError(t, err)
			assert.Equal(t, tc.wantModels, modelIDs(got))
			assert.Equal(t, int32(1), hits.Load())
		})
	}
}

func TestFetchModelsDefaultsToOpenAI(t *testing.T) {
	// No base URL and no claude-ish hints: the request must go to the public
	// OpenAI endpoint. The DNS lookup fails here, which is fine — we only care
	// that the failure is a transport error and not an arg error.
	_, err := fetchModels(t.Context(), "", "test-key", "", "gpt-4o")
	require.Error(t, err)
	assert.NotErrorIs(t, err, http.ErrNotSupported)
}

func TestFetchModelsRejectsBadBaseURL(t *testing.T) {
	cases := []struct{ name, baseURL string }{
		{"relative", "api.example.com/v1"},
		{"no host", "https:///v1"},
		{"bad scheme", "ftp://api.example.com/v1"},
		{"not a url", "://nope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := fetchModels(t.Context(), tc.baseURL, "key", "OpenAI", "m")
			require.Error(t, err)
		})
	}
}

func TestFetchModelsSurfacesProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer server.Close()

	_, err := fetchModels(t.Context(), server.URL, "test-key", "OpenAI", "m")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad key")
}

func TestFetchModelsRedirects(t *testing.T) {
	t.Run("same origin is followed", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/models":
				http.Redirect(w, r, "/models-final", http.StatusTemporaryRedirect)
			case "/models-final":
				assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
				_, _ = w.Write([]byte(`{"data":[{"id":"via-redirect"}]}`))
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		got, err := fetchModels(t.Context(), server.URL+"/v1", "test-key", "OpenAI", "m")
		require.NoError(t, err)
		assert.Equal(t, []string{"via-redirect"}, modelIDs(got))
	})

	t.Run("cross origin is rejected without forwarding key", func(t *testing.T) {
		var targetRequests atomic.Int32
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			targetRequests.Add(1)
			assert.Empty(t, r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"data":[]}`))
		}))
		defer target.Close()

		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+"/models", http.StatusTemporaryRedirect)
		}))
		defer source.Close()

		_, err := fetchModels(t.Context(), source.URL+"/v1", "test-key", "OpenAI", "m")
		require.Error(t, err)
		assert.Zero(t, targetRequests.Load())
	})
}

func TestModelListEndpoint(t *testing.T) {
	cases := []struct {
		name      string
		baseURL   string
		anthropic bool
		gemini    bool
		want      string
	}{
		{"openai base", "https://api.openai.com/v1", false, false, "https://api.openai.com/v1/models"},
		{"bare host", "https://example.com", false, false, "https://example.com/models"},
		{"already models", "https://example.com/v1/models", false, false, "https://example.com/v1/models"},
		{"anthropic adds v1", "https://api.anthropic.com", true, false, "https://api.anthropic.com/v1/models"},
		{"drops query", "https://example.com/v1?x=1", false, false, "https://example.com/v1/models"},
		{"trailing slash", "https://example.com/v1/", false, false, "https://example.com/v1/models"},
		{
			"gemini adds pageSize",
			"https://generativelanguage.googleapis.com/v1beta",
			false,
			true,
			"https://generativelanguage.googleapis.com/v1beta/models?pageSize=1000",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := modelListEndpoint(tc.baseURL, tc.anthropic, tc.gemini)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestIsAnthropicAPI(t *testing.T) {
	cases := []struct {
		api, baseURL, model string
		want                bool
	}{
		{"Anthropic", "", "", true},
		{"OpenAI", "https://anthropic.example", "claude-3", false},
		{"Gemini", "https://x.example", "gemini-pro", false},
		{"", "https://api.anthropic.com", "m", true},
		{"", "https://x.example", "claude-sonnet", true},
		{"", "https://x.example", "m", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, isAnthropicAPI(tc.api, tc.baseURL, tc.model), tc)
	}
}

func TestIsGeminiAPIAndAuth(t *testing.T) {
	cases := []struct {
		api, baseURL string
		want         bool
	}{
		{"Gemini", "", true},
		{"OpenAI", "https://generativelanguage.googleapis.com/v1beta/openai", false},
		{"", "https://generativelanguage.googleapis.com/v1beta/openai", false},
		{"", "https://generativelanguage.googleapis.com/v1beta", true},
		{"", "https://api.openai.com/v1", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, isGeminiAPI(tc.api, tc.baseURL), tc)
	}

	vertex := http.Header{}
	setModelListAuth(vertex, "https://us-central1-aiplatform.googleapis.com/v1", "tok", false, true)
	assert.Equal(t, "Bearer tok", vertex.Get("Authorization"))
	assert.Empty(t, vertex.Get("x-goog-api-key"))
}

func TestCollectModelInfosPrefersIDAndParsesMetadata(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }
	raw := []json.RawMessage{
		json.RawMessage(`{"id":"id","name":"name","display_name":"display"}`),
		json.RawMessage(`{"name":"name-only"}`),
		json.RawMessage(`{"display_name":"display-only"}`),
		json.RawMessage(`{}`),
		json.RawMessage(`{"id":"id","context_length":999}`),
		json.RawMessage(
			`{"id":"or-img","context_length":262144,"top_provider":{"max_completion_tokens":32768},"architecture":{"input_modalities":["text","image"]}}`,
		),
		json.RawMessage(
			`{"id":"or-text","top_provider":{"context_length":131072.0},"architecture":{"input_modalities":["text"]}}`,
		),
		json.RawMessage(
			`{"id":"claude-5","max_input_tokens":1000000,"max_tokens":128000,"capabilities":{"image_input":{"supported":true}}}`,
		),
		json.RawMessage(`{"id":"vllm-m","max_model_len":65536.0,"max_output_tokens":8192.0,"supports_vision":true}`),
		json.RawMessage(
			`{"id":"magpie-img","context_window":200000,"max_output_tokens":64000,"modalities":{"input":["text","image"],"output":["text"]}}`,
		),
		json.RawMessage(
			`{"id":"magpie-text","context_window":131072,"modalities":{"input":["text"],"output":["text"]}}`,
		),
		json.RawMessage(
			`{"id":"custom-qwen","context_window":32768,"max_tokens":8192,"input_modalities":["text","image"]}`,
		),
		json.RawMessage(
			`{"id":"quirky","context_length":"128k","max_tokens":0,"architecture":"qwen2","capabilities":["vision"],"modalities":{"output":["image"]},"supports_vision":null}`,
		),
	}
	got := collectModelInfos(raw, false)
	assert.Equal(t, []configui.ModelInfo{
		{ID: "id"},
		{ID: "name-only"},
		{ID: "display-only"},
		{ID: "or-img", ContextWindow: 262_144, MaxTokens: 32_768, ImageEnabled: boolPtr(true)},
		{ID: "or-text", ContextWindow: 131_072, ImageEnabled: boolPtr(false)},
		{ID: "claude-5", ContextWindow: 1_000_000, MaxTokens: 128_000, ImageEnabled: boolPtr(true)},
		{ID: "vllm-m", ContextWindow: 65_536, MaxTokens: 8_192, ImageEnabled: boolPtr(true)},
		{ID: "magpie-img", ContextWindow: 200_000, MaxTokens: 64_000, ImageEnabled: boolPtr(true)},
		{ID: "magpie-text", ContextWindow: 131_072, ImageEnabled: boolPtr(false)},
		{ID: "custom-qwen", ContextWindow: 32_768, MaxTokens: 8_192, ImageEnabled: boolPtr(true)},
		{ID: "quirky"},
	}, got)

	gem := collectModelInfos([]json.RawMessage{
		json.RawMessage(`{"name":"models/gemini-4-neon","inputTokenLimit":1048576,"outputTokenLimit":65536}`),
	}, true)
	assert.Equal(t, []configui.ModelInfo{
		{ID: "gemini-4-neon", ContextWindow: 1_048_576, MaxTokens: 65_536},
	}, gem)
}
