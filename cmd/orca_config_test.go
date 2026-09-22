package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/orca"
	"github.com/pulseaiclub/phi/internal/project"
)

// orcaTestKey is syntactically plausible and entirely fake. No test in this
// file may use a real key, and none may print one.
const orcaTestKey = "sk-orca-test-only-not-a-real-key"

// orcaTestCatalog is a fake /v1/models payload that covers every capability
// the selector filters on: text-only chat, image-input chat, an embedding
// model, an image generator, a video model, and a reranker.
const orcaTestCatalog = `{"data":[
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
  {"id":"openai/gpt-5.5-mini","object":"model","context_length":128000,
   "supported_endpoint_types":["openai"],
   "architecture":{"input_modalities":["text"]}},
  {"id":"mystery/undeclared","object":"model"}
]}`

// orcaCatalogServer serves a fake inference origin whose /v1/models returns the
// fixture above, and records the Authorization header it saw.
func orcaCatalogServer(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	var seenAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != orca.ModelsPath {
			http.NotFound(w, r)
			return
		}
		seenAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(orcaTestCatalog))
	}))
	t.Cleanup(server.Close)
	return server, &seenAuth
}

// orcaTestHandler builds a configHandler whose credential store lives in a
// temporary phi home and whose origins point at the supplied fake servers.
func orcaTestHandler(t *testing.T, authBase, apiBase string) (*configHandler, *project.Project) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(orca.EnvAPIKey, "")
	t.Setenv(orca.EnvAuthBaseURL, authBase)
	t.Setenv(orca.EnvAPIBaseURL, apiBase)
	t.Setenv(orca.EnvBaseURL, "")

	proj, err := project.Discover("")
	require.NoError(t, err)
	return &configHandler{configPath: filepath.Join(home, ".phi", "config.yaml"), proj: proj}, proj
}

func orcaJSONRequest(method, target, body string) *http.Request {
	req := httptest.NewRequestWithContext(context.Background(), method, target, strings.NewReader(body))
	req.Host = "127.0.0.1:43210"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://127.0.0.1:43210")
	return req
}

// ---------------------------------------------------------------------------
// Model catalog: live discovery, per-capability filtering, degraded fallback.
// ---------------------------------------------------------------------------

func TestOrcaModelsComeFromLiveCatalog(t *testing.T) {
	server, seenAuth := orcaCatalogServer(t)
	h, proj := orcaTestHandler(t, server.URL, server.URL)
	_, err := proj.OrcaStore().SetAPIKey(orcaTestKey, "")
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, orcaJSONRequest(http.MethodPost, "/api/models", `{"api":"OrcaRouter"}`))
	require.Equal(t, http.StatusOK, rr.Code)

	var got orcaModelsResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, "live", got.Source)
	assert.False(t, got.Degraded)
	assert.Equal(t, orca.ModelsPath, mustPath(t, got.CatalogURL))
	assert.Contains(t, got.CatalogURL, "capability=chat")

	// The key stayed on this side of the loopback boundary and went out as a
	// bearer token to the inference origin only.
	assert.Equal(t, "Bearer "+orcaTestKey, *seenAuth)
	assert.NotContains(t, rr.Body.String(), orcaTestKey, "the browser must never receive the key")

	ids := orcaItemIDs(got.Models)
	assert.Contains(t, ids, "openai/gpt-5.5")
	assert.Contains(t, ids, "deepseek/deepseek-v4.1-flash")
	// Text-only chat: non-chat endpoint types are excluded, and so is a model
	// that declares no endpoint types at all (fail closed).
	assert.NotContains(t, ids, "orcarouter/embed-large")
	assert.NotContains(t, ids, "openai/dall-e-9")
	assert.NotContains(t, ids, "openai/sora-9")
	assert.NotContains(t, ids, "jina/jina-rerank-9")
	assert.NotContains(t, ids, "mystery/undeclared")
}

func TestOrcaModelCapabilityFiltering(t *testing.T) {
	server, _ := orcaCatalogServer(t)
	h, proj := orcaTestHandler(t, server.URL, server.URL)
	_, err := proj.OrcaStore().SetAPIKey(orcaTestKey, "")
	require.NoError(t, err)

	cases := []struct {
		name string
		body string
		want []string
		deny []string
	}{
		{
			name: "chat excludes non-text endpoints",
			body: `{"api":"OrcaRouter","capability":"chat"}`,
			want: []string{"openai/gpt-5.5", "openai/gpt-5.5-mini", "deepseek/deepseek-v4.1-flash"},
			deny: []string{"orcarouter/embed-large", "openai/dall-e-9", "openai/sora-9", "jina/jina-rerank-9"},
		},
		{
			name: "image input keeps only declared image modalities",
			body: `{"api":"OrcaRouter","imageInput":true}`,
			want: []string{"deepseek/deepseek-v4.1-flash"},
			deny: []string{"openai/gpt-5.5", "openai/gpt-5.5-mini", "openai/dall-e-9"},
		},
		{
			name: "embedding matches the embeddings endpoint only",
			body: `{"api":"OrcaRouter","capability":"embedding"}`,
			want: []string{"orcarouter/embed-large"},
			deny: []string{"openai/gpt-5.5", "openai/dall-e-9"},
		},
		{
			name: "image generation matches image-generation only",
			body: `{"api":"OrcaRouter","capability":"image-generation"}`,
			want: []string{"openai/dall-e-9"},
			deny: []string{"openai/gpt-5.5", "openai/sora-9"},
		},
		{
			name: "video matches openai-video only",
			body: `{"api":"OrcaRouter","capability":"video"}`,
			want: []string{"openai/sora-9"},
			deny: []string{"openai/dall-e-9", "openai/gpt-5.5"},
		},
		{
			name: "rerank matches jina-rerank only",
			body: `{"api":"OrcaRouter","capability":"rerank"}`,
			want: []string{"jina/jina-rerank-9"},
			deny: []string{"openai/gpt-5.5", "orcarouter/embed-large"},
		},
		{
			name: "unknown capability is rejected, never silently widened",
			body: `{"api":"OrcaRouter","capability":"telepathy"}`,
			want: nil,
			deny: []string{"openai/gpt-5.5"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, orcaJSONRequest(http.MethodPost, "/api/models", tc.body))
			if tc.want == nil {
				assert.Equal(t, http.StatusBadRequest, rr.Code)
				assert.NotContains(t, rr.Body.String(), "openai/gpt-5.5")
				return
			}
			require.Equal(t, http.StatusOK, rr.Code)
			var got orcaModelsResponse
			require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
			ids := orcaItemIDs(got.Models)
			for _, want := range tc.want {
				assert.Contains(t, ids, want)
			}
			for _, deny := range tc.deny {
				assert.NotContains(t, ids, deny)
			}
		})
	}
}

func TestOrcaModelsDegradedFallbackIsLabelled(t *testing.T) {
	// A dead inference origin: discovery fails, so the verified seed must be
	// served with an explicit degraded marker rather than a free-text field.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)

	h, _ := orcaTestHandler(t, server.URL, server.URL)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, orcaJSONRequest(http.MethodPost, "/api/models", `{"api":"OrcaRouter"}`))
	require.Equal(t, http.StatusOK, rr.Code)

	var got orcaModelsResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, "seed", got.Source)
	assert.True(t, got.Degraded, "a fallback list must be labeled degraded")
	assert.NotEmpty(t, got.Warning)
	// The fallback is the verified seed, not an empty list and not a
	// hand-written one-off.
	assert.Equal(t, orca.SeedIDs(), orcaItemIDs(got.Models))
}

func TestOrcaModelsRejectsNonLoopbackCaller(t *testing.T) {
	server, _ := orcaCatalogServer(t)
	h, _ := orcaTestHandler(t, server.URL, server.URL)
	req := orcaJSONRequest(http.MethodPost, "/api/models", `{"api":"OrcaRouter"}`)
	req.Host = "evil.example"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

// ---------------------------------------------------------------------------
// Credential seam: the API-key adapter over HTTP.
// ---------------------------------------------------------------------------

func TestOrcaCredentialAPIKeyRoundTrip(t *testing.T) {
	server, _ := orcaCatalogServer(t)
	h, proj := orcaTestHandler(t, server.URL, server.URL)

	// Initially absent.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, orcaJSONRequest(http.MethodGet, "/api/orca/credential", ""))
	require.Equal(t, http.StatusOK, rr.Code)
	var view orcaCredentialResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &view))
	assert.False(t, view.Present)
	assert.Equal(t, orca.ConsoleKeysURL, view.ConsoleURL)

	// Save.
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, orcaJSONRequest(http.MethodPost, "/api/orca/credential",
		`{"apiKey":"`+orcaTestKey+`"}`))
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &view))
	assert.True(t, view.Present)
	assert.Equal(t, string(llm.CredentialSourceAPIKey), view.Source)
	assert.NotContains(t, rr.Body.String(), orcaTestKey, "the key must never be echoed back")
	assert.Contains(t, view.Masked, "…", "the page shows a masked key only")
	assert.NotEqual(t, orcaTestKey, view.Masked)

	// The store is what the provider reads, and it agrees with the HTTP view.
	stored, err := proj.OrcaStore().Credential(t.Context())
	require.NoError(t, err)
	assert.Equal(t, orcaTestKey, stored.APIKey)

	// Clear.
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, orcaJSONRequest(http.MethodPost, "/api/orca/credential", `{"clear":true}`))
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &view))
	assert.False(t, view.Present)
	_, err = proj.OrcaStore().Credential(t.Context())
	assert.ErrorIs(t, err, orca.ErrNoCredential)
}

func TestOrcaCredentialRejectsEmptyWrite(t *testing.T) {
	server, _ := orcaCatalogServer(t)
	h, _ := orcaTestHandler(t, server.URL, server.URL)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, orcaJSONRequest(http.MethodPost, "/api/orca/credential", `{}`))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ---------------------------------------------------------------------------
// Connect flow over HTTP: begin, generation guard, cancel, abandon.
// ---------------------------------------------------------------------------

func TestOrcaConnectBeginReturnsAuthorizeURLWithoutVerifier(t *testing.T) {
	server, _ := orcaCatalogServer(t)
	h, _ := orcaTestHandler(t, server.URL, server.URL)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, orcaJSONRequest(http.MethodPost, "/api/orca/connect", `{"action":"begin"}`))
	require.Equal(t, http.StatusOK, rr.Code)

	var state orcaConnectState
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &state))
	assert.True(t, state.Busy, "the login lock is held while waiting for the code")
	assert.NotZero(t, state.Attempt)
	require.Contains(t, state.AuthorizeURL, orca.AuthorizePath)
	assert.Contains(t, state.AuthorizeURL, "code_challenge_method=S256")
	assert.NotContains(t, state.AuthorizeURL, "code_verifier",
		"the verifier must not leave the process before the exchange")
	assert.NotContains(t, rr.Body.String(), "code_verifier")

	// Cancel through the same generation.
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, orcaJSONRequest(http.MethodPost, "/api/orca/connect",
		`{"action":"cancel","attempt":`+itoa(state.Attempt)+`}`))
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &state))
	assert.False(t, state.Busy, "cancel must release the lock")
	assert.Equal(t, string(orca.StateCanceled), state.State)
}

func TestOrcaConnectAbandonReleasesLock(t *testing.T) {
	server, _ := orcaCatalogServer(t)
	h, _ := orcaTestHandler(t, server.URL, server.URL)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, orcaJSONRequest(http.MethodPost, "/api/orca/connect", `{"action":"begin"}`))
	require.Equal(t, http.StatusOK, rr.Code)
	var state orcaConnectState
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &state))

	// pagehide: the page is going away and must not leave the server busy.
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, orcaJSONRequest(http.MethodPost, "/api/orca/connect",
		`{"action":"abandon","attempt":`+itoa(state.Attempt)+`}`))
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &state))
	assert.False(t, state.Busy)

	// A second login must be able to start without a remount.
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, orcaJSONRequest(http.MethodPost, "/api/orca/connect", `{"action":"begin"}`))
	require.Equal(t, http.StatusOK, rr.Code)
	var second orcaConnectState
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &second))
	assert.True(t, second.Busy)
	assert.Greater(t, second.Attempt, state.Attempt, "attempts are monotonic")

	// A stale abandon from the abandoned generation must not touch the new
	// login.
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, orcaJSONRequest(http.MethodPost, "/api/orca/connect",
		`{"action":"abandon","attempt":`+itoa(state.Attempt)+`}`))
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &second))
	assert.True(t, second.Busy, "a stale pagehide must not cancel a newer login")
}

func TestOrcaConnectStaleCodeIsRejected(t *testing.T) {
	server, _ := orcaCatalogServer(t)
	h, _ := orcaTestHandler(t, server.URL, server.URL)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, orcaJSONRequest(http.MethodPost, "/api/orca/connect", `{"action":"begin"}`))
	require.Equal(t, http.StatusOK, rr.Code)
	var state orcaConnectState
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &state))

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, orcaJSONRequest(http.MethodPost, "/api/orca/connect",
		`{"action":"code","attempt":`+itoa(state.Attempt+7)+`,"code":"stale"}`))
	assert.Equal(t, http.StatusConflict, rr.Code)
}

func TestOrcaConnectUnknownActionRejected(t *testing.T) {
	server, _ := orcaCatalogServer(t)
	h, _ := orcaTestHandler(t, server.URL, server.URL)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, orcaJSONRequest(http.MethodPost, "/api/orca/connect", `{"action":"dance"}`))
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestOrcaConnectStatusRecoversAfterReload(t *testing.T) {
	server, _ := orcaCatalogServer(t)
	h, _ := orcaTestHandler(t, server.URL, server.URL)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, orcaJSONRequest(http.MethodPost, "/api/orca/connect", `{"action":"begin"}`))
	require.Equal(t, http.StatusOK, rr.Code)

	// A reloaded page asks for the state instead of starting a second login.
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, orcaJSONRequest(http.MethodGet, "/api/orca/connect/status", ""))
	require.Equal(t, http.StatusOK, rr.Code)
	var state orcaConnectState
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &state))
	assert.True(t, state.Busy)
	assert.NotZero(t, state.Attempt)
	assert.NotContains(t, rr.Body.String(), "code_verifier")
}

// ---------------------------------------------------------------------------
// Wiring: the page carries both authentication choices.
// ---------------------------------------------------------------------------

func TestConfigPageExposesBothAuthenticationMethods(t *testing.T) {
	h, _ := orcaTestHandler(t, "https://www.orcarouter.ai", "https://api.orcarouter.ai")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))
	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()

	// API key entry point.
	assert.Contains(t, body, `id="orcaApiKey"`)
	assert.Contains(t, body, `id="orcaSaveKey"`)
	assert.Contains(t, body, `id="orcaClearKey"`)
	assert.Contains(t, body, `type="password"`)
	// PKCE entry point, side by side with the key field.
	assert.Contains(t, body, `id="orcaConnect"`)
	assert.Contains(t, body, `id="orcaCancel"`)
	assert.Contains(t, body, `id="orcaCode"`)
	assert.Contains(t, body, `id="orcaSection"`)
	// Endpoints the page talks to.
	assert.Contains(t, body, "/api/orca/credential")
	assert.Contains(t, body, "/api/orca/connect")
	assert.Contains(t, body, "/api/models")
	// The pagehide cleanup path must exist, or a closed tab leaves the login
	// lock held.
	assert.Contains(t, body, "pagehide")
	assert.Contains(t, body, "keepalive")
}

func TestConfigPageModelSelectorFiltersByCapability(t *testing.T) {
	h, _ := orcaTestHandler(t, "https://www.orcarouter.ai", "https://api.orcarouter.ai")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))
	body := rr.Body.String()

	// The selector is fed by the discovery endpoint, not by a text field.
	assert.Contains(t, body, `"chat"`)
	assert.Contains(t, body, `"image-input"`)
	assert.Contains(t, body, "imageInput")
	assert.Contains(t, body, "degraded")
	// It is a custom listbox rather than a native <select>: the native popup is
	// drawn by the OS, so an open, capability-filtered list could not be shown
	// or verified in the rendered page.
	assert.Contains(t, body, `role: "listbox"`)
	assert.Contains(t, body, "model-picker-panel")
	assert.Contains(t, body, "model-picker-trigger")
	assert.Contains(t, body, "aria-expanded")
	// An incompatible selection is cleared, not silently kept.
	assert.Contains(t, body, "closeModelPicker(row)")
}

// ---------------------------------------------------------------------------
// i18n parity: every published locale must carry the OrcaRouter strings.
// ---------------------------------------------------------------------------

func TestConfigPageOrcaStringsHaveEveryLocale(t *testing.T) {
	h, _ := orcaTestHandler(t, "https://www.orcarouter.ai", "https://api.orcarouter.ai")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))
	body := rr.Body.String()

	for _, key := range []string{
		"apiOrcaRouter", "orcaHeading", "orcaApiKeyLabel", "orcaSaveKey", "orcaClearKey",
		"orcaConnect", "orcaCancel", "orcaCodeLabel", "orcaConsole", "fetchModelsDegraded",
		"orcaApiKeyTitle", "orcaPkceTitle", "orcaApiKeyHelp", "orcaPkceHelp", "orcaNotConnected",
	} {
		assert.GreaterOrEqualf(t, strings.Count(body, key+":"), 2,
			"locale key %q must be defined in both en and zh", key)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func orcaItemIDs(items []orcaModelItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func mustPath(t *testing.T, raw string) string {
	t.Helper()
	require.NotEmpty(t, raw)
	idx := strings.Index(raw, "://")
	require.GreaterOrEqual(t, idx, 0)
	rest := raw[idx+3:]
	slash := strings.Index(rest, "/")
	require.GreaterOrEqual(t, slash, 0)
	path := rest[slash:]
	if q := strings.Index(path, "?"); q >= 0 {
		path = path[:q]
	}
	return path
}

func itoa(v int) string { return strconv.Itoa(v) }
