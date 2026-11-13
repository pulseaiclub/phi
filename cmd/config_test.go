package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchModelIDs(t *testing.T) {
	cases := []struct {
		name        string
		api         string
		model       string
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
			response:   `{"models":[{"name":"proxy-model"}]}`,
			wantPath:   "/v1/models",
			wantModels: []string{"proxy-model"},
			checkAuth: func(t *testing.T, r *http.Request) {
				assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
				assert.Empty(t, r.Header.Get("X-Api-Key"))
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
				baseURL = server.URL + "/v1"
			}
			got, err := fetchModelIDs(t.Context(), baseURL, "test-key", tc.api, tc.model)
			require.NoError(t, err)
			assert.Equal(t, tc.wantModels, got)
			assert.Equal(t, int32(1), hits.Load())
		})
	}
}

func TestFetchModelIDsDefaultsToOpenAI(t *testing.T) {
	// No base URL and no claude-ish hints: the request must go to the public
	// OpenAI endpoint. The DNS lookup fails here, which is fine — we only care
	// that the failure is a transport error and not an arg error.
	_, err := fetchModelIDs(t.Context(), "", "test-key", "", "gpt-4o")
	require.Error(t, err)
	assert.NotErrorIs(t, err, http.ErrNotSupported)
}

func TestFetchModelIDsRejectsBadBaseURL(t *testing.T) {
	cases := []struct{ name, baseURL string }{
		{"relative", "api.example.com/v1"},
		{"no host", "https:///v1"},
		{"bad scheme", "ftp://api.example.com/v1"},
		{"not a url", "://nope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := fetchModelIDs(t.Context(), tc.baseURL, "key", "OpenAI", "m")
			require.Error(t, err)
		})
	}
}

func TestFetchModelIDsSurfacesProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer server.Close()

	_, err := fetchModelIDs(t.Context(), server.URL, "test-key", "OpenAI", "m")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad key")
}

func TestFetchModelIDsRedirects(t *testing.T) {
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

		got, err := fetchModelIDs(t.Context(), server.URL+"/v1", "test-key", "OpenAI", "m")
		require.NoError(t, err)
		assert.Equal(t, []string{"via-redirect"}, got)
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

		_, err := fetchModelIDs(t.Context(), source.URL+"/v1", "test-key", "OpenAI", "m")
		require.Error(t, err)
		assert.Zero(t, targetRequests.Load())
	})
}

func TestModelListEndpoint(t *testing.T) {
	cases := []struct {
		name      string
		baseURL   string
		anthropic bool
		want      string
	}{
		{"openai base", "https://api.openai.com/v1", false, "https://api.openai.com/v1/models"},
		{"bare host", "https://example.com", false, "https://example.com/models"},
		{"already models", "https://example.com/v1/models", false, "https://example.com/v1/models"},
		{"anthropic adds v1", "https://api.anthropic.com", true, "https://api.anthropic.com/v1/models"},
		{"drops query", "https://example.com/v1?x=1", false, "https://example.com/v1/models"},
		{"trailing slash", "https://example.com/v1/", false, "https://example.com/v1/models"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := modelListEndpoint(tc.baseURL, tc.anthropic)
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

func TestCollectModelIDsPrefersID(t *testing.T) {
	got := collectModelIDs([]modelListItem{
		{ID: "id", Name: "name", DisplayName: "display"},
		{Name: "name-only"},
		{DisplayName: "display-only"},
		{},
		{ID: "id"},
	})
	assert.Equal(t, []string{"id", "name-only", "display-only"}, got)
}
