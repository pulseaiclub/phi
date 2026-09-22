package orca

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// catalogFixture covers one model per capability the selector filters on, plus
// a model that declares nothing at all: an undeclared model must fail closed
// rather than being admitted on a name guess.
const catalogFixture = `{"data":[
  {"id":"openai/gpt-5.5","object":"model","context_length":272000,
   "supported_endpoint_types":["openai","anthropic"],
   "architecture":{"input_modalities":["text"]}},
  {"id":"deepseek/deepseek-v4.1-flash","object":"model","context_length":131072,
   "supported_endpoint_types":["openai"],
   "architecture":{"input_modalities":["text","image"]}},
  {"id":"orcarouter/embed-large","object":"model",
   "supported_endpoint_types":["embeddings"],
   "architecture":{"input_modalities":["text"]}},
  {"id":"openai/dall-e-9","object":"model",
   "supported_endpoint_types":["image-generation"],
   "architecture":{"input_modalities":["text"]}},
  {"id":"openai/sora-9","object":"model",
   "supported_endpoint_types":["openai-video"],
   "architecture":{"input_modalities":["text"]}},
  {"id":"jina/jina-rerank-9","object":"model",
   "supported_endpoint_types":["jina-rerank"],
   "architecture":{"input_modalities":["text"]}},
  {"id":"mystery/undeclared","object":"model"}
]}`

func TestCatalogParsingFiltersPerCapability(t *testing.T) {
	cases := []struct {
		capability Capability
		want       []string
	}{
		{CapabilityChat, []string{"deepseek/deepseek-v4.1-flash", "openai/gpt-5.5"}},
		{CapabilityImageInput, []string{"deepseek/deepseek-v4.1-flash"}},
		{CapabilityEmbedding, []string{"orcarouter/embed-large"}},
		{CapabilityImageGen, []string{"openai/dall-e-9"}},
		{CapabilityVideo, []string{"openai/sora-9"}},
		{CapabilityRerank, []string{"jina/jina-rerank-9"}},
	}
	for _, tc := range cases {
		t.Run(string(tc.capability), func(t *testing.T) {
			models, err := ParseCatalogForTest([]byte(catalogFixture), tc.capability)
			require.NoError(t, err)
			assert.Equal(t, tc.want, ModelIDs(models))
			// An undeclared model is never admitted, whatever the capability.
			assert.NotContains(t, ModelIDs(models), "mystery/undeclared")
		})
	}
}

// A server that ignores (or widens) the capability parameter must not leak an
// incompatible model into a dropdown: the local check is the authority.
func TestCatalogParsingIgnoresServerSideWidening(t *testing.T) {
	models, err := ParseCatalogForTest([]byte(catalogFixture), CapabilityImageInput)
	require.NoError(t, err)
	for _, m := range models {
		assert.Contains(t, m.InputModalities, "image",
			"%s was admitted for image input without declaring it", m.ID)
	}
}

func TestCatalogParsingKeepsVendorNamespaceAndMetadata(t *testing.T) {
	models, err := ParseCatalogForTest([]byte(catalogFixture), CapabilityChat)
	require.NoError(t, err)
	m, ok := Find(models, "openai/gpt-5.5")
	require.True(t, ok)
	assert.Equal(t, "openai/gpt-5.5", m.ID, "the vendor/model namespace is preserved verbatim")
	assert.Equal(t, 272_000, m.ContextWindow)
	assert.Equal(t, []string{"openai", "anthropic"}, m.EndpointTypes)
}

func TestCatalogParsingRejectsMalformedAndOversized(t *testing.T) {
	_, err := ParseCatalogForTest([]byte(`{"data":`), CapabilityChat)
	require.Error(t, err)

	_, ok := Find(SeedModels(), "no/such-model")
	assert.False(t, ok)
}

// The seed is the outage fallback: it must keep its verified reasoning ladder
// so restoring a fallback model does not silently drop that metadata.
func TestSeedCatalogCarriesVerifiedMetadata(t *testing.T) {
	seeds := SeedModels()
	require.NotEmpty(t, seeds)
	for _, m := range seeds {
		assert.Contains(t, m.ID, "/", "%s must keep its vendor namespace", m.ID)
		assert.NotEmpty(t, m.EndpointTypes, "%s must declare an endpoint type", m.ID)
		assert.Equal(t, CatalogSourceSeed, m.Source)
	}

	assert.Equal(t, []string{"low", "medium", "high", "xhigh"},
		SeedReasoningLevels("openai/gpt-5.5"),
		"GPT-5.5 supports low/medium/high/xhigh")
	assert.Nil(t, SeedReasoningLevels("deepseek/deepseek-v4-pro"),
		"a reasoning ladder must never be invented for a model that has none")
	assert.Nil(t, SeedReasoningLevels("no/such-model"))

	// The seed is usable for chat and keeps a per-capability view.
	assert.NotEmpty(t, Filter(seeds, CapabilityChat))
	assert.NotEmpty(t, Filter(seeds, CapabilityImageInput))
	assert.Equal(t, SeedIDs(), ModelIDs(seeds))
}

// A live answer is authoritative: the seed is never merged into it.
func TestLiveCatalogNeverMergesTheSeed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(catalogFixture))
	}))
	t.Cleanup(server.Close)

	origins := testOrigins(t, server.URL, server.URL)
	live := origins.LiveCatalogWithFallback(t.Context(), nil, fakeKey, CapabilityChat)
	require.False(t, live.Degraded)
	assert.Equal(t, CatalogSourceLive, live.Source)
	// A seed-only model must not appear once the origin has answered.
	assert.NotContains(t, ModelIDs(live.Models), "anthropic/claude-opus-4.8")
	assert.Equal(t, []string{"deepseek/deepseek-v4.1-flash", "openai/gpt-5.5"}, ModelIDs(live.Models))
}

func TestLiveCatalogFallsBackToSeedWhenDiscoveryFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	origins := testOrigins(t, server.URL, server.URL)
	fallback := origins.LiveCatalogWithFallback(t.Context(), nil, fakeKey, CapabilityChat)
	assert.True(t, fallback.Degraded, "a fallback must be labeled degraded")
	assert.Equal(t, CatalogSourceSeed, fallback.Source)
	assert.NotEmpty(t, fallback.Warning)
	assert.Equal(t, SeedIDs(), ModelIDs(fallback.Models))
}
