package orca

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

// fakeKey is a syntactically plausible but entirely fake key. No test in this
// package uses a real credential.
const fakeKey = "sk-orca-test-only-not-a-real-key"

func testOrigins(t *testing.T, authBase, apiBase string) Origins {
	t.Helper()
	origins, err := ResolveOrigins(func(name string) string {
		switch name {
		case EnvAuthBaseURL:
			return authBase
		case EnvAPIBaseURL:
			return apiBase
		}
		return ""
	})
	require.NoError(t, err)
	return origins
}

// ---------------------------------------------------------------------------
// Origins: auth and inference never derive from one another.
// ---------------------------------------------------------------------------

func TestResolveOriginsDefaults(t *testing.T) {
	origins, err := ResolveOrigins(func(string) string { return "" })
	require.NoError(t, err)
	assert.Equal(t, "https://www.orcarouter.ai", origins.AuthBase)
	assert.Equal(t, "https://api.orcarouter.ai", origins.APIBase)
	assert.Equal(t, "https://www.orcarouter.ai/api/v1/auth/keys", origins.ExchangeURL())
	assert.Equal(t, "https://api.orcarouter.ai/v1/models", origins.ModelsURL())
	assert.Equal(t, "https://api.orcarouter.ai/v1", origins.InferenceBaseURL())
}

func TestResolveOriginsSharedBaseIsFallback(t *testing.T) {
	env := map[string]string{EnvBaseURL: "https://self.example"}
	origins, err := ResolveOrigins(func(k string) string { return env[k] })
	require.NoError(t, err)
	assert.Equal(t, "https://self.example", origins.AuthBase)
	assert.Equal(t, "https://self.example", origins.APIBase)
}

func TestResolveOriginsExplicitOverridesWin(t *testing.T) {
	env := map[string]string{
		EnvBaseURL:     "https://shared.example",
		EnvAuthBaseURL: "https://auth.example",
		EnvAPIBaseURL:  "https://api.example",
	}
	origins, err := ResolveOrigins(func(k string) string { return env[k] })
	require.NoError(t, err)
	assert.Equal(t, "https://auth.example", origins.AuthBase)
	assert.Equal(t, "https://api.example", origins.APIBase)
}

func TestNormalizeOriginRequiresHTTPSOffLoopback(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{name: "https remote", raw: "https://www.orcarouter.ai"},
		{name: "http loopback", raw: "http://127.0.0.1:8080"},
		{name: "http localhost", raw: "http://localhost:3000"},
		{name: "http remote rejected", raw: "http://api.example.com", wantErr: true},
		{name: "userinfo rejected", raw: "https://user:pass@example.com", wantErr: true},
		{name: "query rejected", raw: "https://example.com?token=1", wantErr: true},
		{name: "no scheme rejected", raw: "example.com", wantErr: true},
		{name: "ftp rejected", raw: "ftp://example.com", wantErr: true},
		{name: "empty rejected", raw: "  ", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeOrigin(tc.raw)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.False(t, strings.HasSuffix(got, "/"), "trailing slash should be trimmed")
		})
	}
}

// The single most common integration mistake: /v1/auth/keys on the inference
// origin. It must never be constructed.
func TestExchangeNeverUsesInferenceOrigin(t *testing.T) {
	origins, err := ResolveOrigins(func(string) string { return "" })
	require.NoError(t, err)
	assert.NotContains(t, origins.ExchangeURL(), "api.orcarouter.ai")
	assert.Contains(t, origins.ExchangeURL(), "www.orcarouter.ai/api/v1/auth/keys")
	u, err := url.Parse(origins.ExchangeURL())
	require.NoError(t, err)
	assert.Equal(t, "www.orcarouter.ai", u.Host)
	assert.Equal(t, "/api/v1/auth/keys", u.Path, "the exchange path must keep its /api prefix")
}

// ---------------------------------------------------------------------------
// PKCE primitives.
// ---------------------------------------------------------------------------

func TestNewPKCEProducesFreshMaterial(t *testing.T) {
	seen := make(map[string]struct{})
	for range 50 {
		p, err := NewPKCE()
		require.NoError(t, err)
		_, dup := seen[p.Verifier()]
		require.False(t, dup, "verifier repeated across attempts")
		seen[p.Verifier()] = struct{}{}
		assert.NotEqual(t, p.Verifier(), p.State())
	}
}

func TestChallengeIsUnpaddedBase64URLSHA256(t *testing.T) {
	p, err := NewPKCE()
	require.NoError(t, err)

	sum := sha256.Sum256([]byte(p.Verifier()))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	assert.Equal(t, want, p.Challenge())
	assert.NotContains(t, p.Challenge(), "=", "challenge must be unpadded")
	assert.NotContains(t, p.Challenge(), "+")
	assert.NotContains(t, p.Challenge(), "/")
	// The verifier must not be recoverable from the challenge.
	assert.NotEqual(t, p.Verifier(), p.Challenge())
}

func TestVerifierLengthInRFC7636Range(t *testing.T) {
	p, err := NewPKCE()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(p.Verifier()), 43)
	assert.LessOrEqual(t, len(p.Verifier()), 128)
}

func TestMatchesStateIsExact(t *testing.T) {
	p, err := NewPKCE()
	require.NoError(t, err)
	assert.True(t, p.MatchesState(p.State()))
	assert.False(t, p.MatchesState(""))
	assert.False(t, p.MatchesState(p.State()+"x"))
	assert.False(t, p.MatchesState(strings.ToUpper(p.State())))
}

func TestAuthorizeURLShape(t *testing.T) {
	origins := testOrigins(t, "https://www.orcarouter.ai", "https://api.orcarouter.ai")
	p, err := NewPKCE()
	require.NoError(t, err)

	raw, err := origins.AuthorizeURL(p, AuthorizeOptions{
		CallbackURL: CallbackOOB,
		AppName:     "phi",
		Scope:       ScopeAPI,
	})
	require.NoError(t, err)

	u, err := url.Parse(raw)
	require.NoError(t, err)
	assert.Equal(t, "www.orcarouter.ai", u.Host)
	assert.Equal(t, AuthorizePath, u.Path)
	q := u.Query()
	assert.Equal(t, "oob", q.Get("callback_url"))
	assert.Equal(t, p.Challenge(), q.Get("code_challenge"))
	assert.Equal(t, "S256", q.Get("code_challenge_method"))
	assert.Equal(t, p.State(), q.Get("state"))
	assert.Equal(t, "api", q.Get("scope"))
	assert.Equal(t, "phi", q.Get("app_name"))
	// The verifier must never appear in the URL.
	assert.NotContains(t, raw, p.Verifier())
	assert.NotContains(t, raw, "code_verifier")
}

func TestAuthorizeURLDefaultsScopeAndRejectsBadCallback(t *testing.T) {
	origins := testOrigins(t, "https://www.orcarouter.ai", "https://api.orcarouter.ai")
	p, err := NewPKCE()
	require.NoError(t, err)

	raw, err := origins.AuthorizeURL(p, AuthorizeOptions{CallbackURL: CallbackOOB})
	require.NoError(t, err)
	assert.Contains(t, raw, "scope=api")

	_, err = origins.AuthorizeURL(p, AuthorizeOptions{})
	require.Error(t, err, "callback_url is required")

	_, err = origins.AuthorizeURL(p, AuthorizeOptions{CallbackURL: "http://evil.example/cb"})
	require.Error(t, err, "non-loopback http callback must be rejected")

	_, err = origins.AuthorizeURL(p, AuthorizeOptions{CallbackURL: "https://my.app/cb"})
	require.NoError(t, err, "https callbacks are allowed on any host and port")
}

func TestNormalizeCallbackURLRules(t *testing.T) {
	cases := []struct {
		raw     string
		wantErr bool
	}{
		{raw: "http://127.0.0.1:51733/cb"},
		{raw: "http://localhost:1234/cb"},
		{raw: "https://example.com:8443/cb"},
		{raw: "https://example.com"},
		{raw: "http://example.com/cb", wantErr: true},
		{raw: "https://user:pw@example.com/cb", wantErr: true},
		{raw: "https://example.com/cb#frag", wantErr: true},
		{raw: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			_, err := NormalizeCallbackURL(tc.raw)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

// ---------------------------------------------------------------------------
// Credential store: one seam, two adapters, generation-safe 401.
// ---------------------------------------------------------------------------

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orcarouter.json")
	t.Setenv(EnvAPIKey, "")
	return NewStore(path), path
}

// Both adapters must produce the same credential shape, and the downstream
// consumer must not care which one ran.
func TestBothAdaptersProduceSameCredentialShape(t *testing.T) {
	store, _ := newTestStore(t)

	viaKey, err := store.SetAPIKey(fakeKey, "")
	require.NoError(t, err)

	store2, _ := newTestStore(t)
	viaPKCE, err := store2.SetPKCEKey(fakeKey, "user-1")
	require.NoError(t, err)

	assert.Equal(t, viaKey.APIKey, viaPKCE.APIKey)
	assert.Equal(t, llm.CredentialSourceAPIKey, viaKey.Source)
	assert.Equal(t, llm.CredentialSourcePKCE, viaPKCE.Source)
	// Downstream only reads APIKey; the source is display-only metadata.
	assert.Equal(t, "user-1", viaPKCE.AccountID)
}

func TestStorePersistsAcrossRestart(t *testing.T) {
	store, path := newTestStore(t)
	_, err := store.SetPKCEKey(fakeKey, "user-42")
	require.NoError(t, err)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "credential file must be owner-only")

	// A fresh store over the same file is what a restart looks like: the key
	// is reused rather than re-authorized.
	reopened := NewStore(path)
	cred, err := reopened.Credential(t.Context())
	require.NoError(t, err)
	assert.Equal(t, fakeKey, cred.APIKey)
	assert.Equal(t, "user-42", cred.AccountID)
	assert.Equal(t, llm.CredentialSourcePKCE, cred.Source)
}

func TestStoreClearRemovesCredential(t *testing.T) {
	store, path := newTestStore(t)
	_, err := store.SetAPIKey(fakeKey, "")
	require.NoError(t, err)

	require.NoError(t, store.Clear())
	_, err = store.Credential(t.Context())
	require.ErrorIs(t, err, ErrNoCredential)

	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "the file should be gone after Clear")
}

func TestStoreStatusMasksTheSecret(t *testing.T) {
	store, _ := newTestStore(t)
	_, err := store.SetAPIKey(fakeKey, "")
	require.NoError(t, err)

	st := store.Status(t.Context())
	require.True(t, st.Present)
	assert.NotEqual(t, fakeKey, st.Masked)
	assert.NotContains(t, st.Masked, fakeKey)
	assert.True(t, strings.HasPrefix(st.Masked, "sk-orc"))
	assert.True(t, strings.HasSuffix(st.Masked, "-key"))
}

func TestMaskSecretNeverRevealsShortValues(t *testing.T) {
	assert.Empty(t, llm.MaskSecret(""))
	assert.Equal(t, "••••", llm.MaskSecret("short"))
	assert.Equal(t, "••••", llm.MaskSecret("12345678901"))
	assert.Equal(t, "abcdef…wxyz", llm.MaskSecret("abcdefghijklmnopwxyz"))
}

func TestStoreEnvironmentOverrideWins(t *testing.T) {
	store, _ := newTestStore(t)
	_, err := store.SetAPIKey("sk-orca-from-file", "")
	require.NoError(t, err)
	t.Setenv(EnvAPIKey, "sk-orca-from-env")

	cred, err := store.Credential(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "sk-orca-from-env", cred.APIKey)
	assert.Equal(t, llm.CredentialSourceAPIKey, cred.Source)
}

func TestStoreCorruptFileIsReportedNotSilentlyEmpty(t *testing.T) {
	store, path := newTestStore(t)
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))

	_, err := store.Credential(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse")
}

// A 401 marks the exact generation that made the request. A late failure from
// an old request must never mark a newly reauthorized credential as broken.
func TestMarkUnauthorizedIsGenerationSafe(t *testing.T) {
	store, _ := newTestStore(t)
	first, err := store.SetAPIKey(fakeKey, "")
	require.NoError(t, err)

	// A new login replaces the credential while the old request is in flight.
	second, err := store.SetPKCEKey("sk-orca-newer-key", "user-2")
	require.NoError(t, err)
	assert.Greater(t, second.Generation, first.Generation)

	// The stale 401 must be dropped.
	assert.False(t, store.MarkUnauthorized(first.Generation),
		"a failure from a superseded generation must not be recorded")

	st := store.Status(t.Context())
	assert.False(t, st.NeedsReauth, "the new credential must stay usable")

	// The current generation is marked, and the key is kept.
	assert.True(t, store.MarkUnauthorized(second.Generation))
	st = store.Status(t.Context())
	assert.True(t, st.NeedsReauth)
	cred, err := store.Credential(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "sk-orca-newer-key", cred.APIKey, "the old secret must not be deleted")
}

func TestMarkUnauthorizedSurvivesRestart(t *testing.T) {
	store, path := newTestStore(t)
	cred, err := store.SetPKCEKey(fakeKey, "user-3")
	require.NoError(t, err)
	require.True(t, store.MarkUnauthorized(cred.Generation))

	reopened := NewStore(path)
	st := reopened.Status(t.Context())
	assert.True(t, st.NeedsReauth)
}

func TestStoreRejectsEmptyKey(t *testing.T) {
	store, _ := newTestStore(t)
	_, err := store.SetAPIKey("   ", "")
	require.Error(t, err)
	_, err = store.SetPKCEKey("", "")
	require.Error(t, err)
}

func TestStoreWithoutPathWorksFromEnvironment(t *testing.T) {
	t.Setenv(EnvAPIKey, "sk-orca-env-only")
	store := NewStore("")
	cred, err := store.Credential(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "sk-orca-env-only", cred.APIKey)
}

// ---------------------------------------------------------------------------
// Exchange.
// ---------------------------------------------------------------------------

func exchangeServer(t *testing.T, handler http.HandlerFunc) Origins {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return testOrigins(t, server.URL, server.URL)
}

func TestExchangeSendsVerifierInBodyOnly(t *testing.T) {
	var gotPath, gotBody, gotQuery string
	var gotMethod string
	origins := exchangeServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":"` + fakeKey + `","user_id":"u1","scope":"api"}`))
	})

	p, err := NewPKCE()
	require.NoError(t, err)
	result, err := origins.Exchange(t.Context(), nil, "code-abc", p.Verifier())
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, ExchangePath, gotPath)
	assert.Empty(t, gotQuery, "the verifier and code must not ride in the query string")
	assert.Equal(t, "/api/v1/auth/keys", gotPath, "the exchange path must not be mistaken for the inference path")

	var sent map[string]string
	require.NoError(t, json.Unmarshal([]byte(gotBody), &sent))
	assert.Equal(t, "code-abc", sent["code"])
	assert.Equal(t, p.Verifier(), sent["code_verifier"])
	assert.Equal(t, "S256", sent["code_challenge_method"])

	assert.Equal(t, fakeKey, result.APIKey)
	assert.Equal(t, "u1", result.UserID)
	assert.Equal(t, "api", result.Scope)
}

func TestExchangeReadsGrantedScopeNotRequested(t *testing.T) {
	origins := exchangeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		// The user asked for connector; the workspace role only allowed api.
		_, _ = w.Write([]byte(`{"key":"` + fakeKey + `","user_id":"u1","scope":"api"}`))
	})
	p, err := NewPKCE()
	require.NoError(t, err)
	result, err := origins.Exchange(t.Context(), nil, "code", p.Verifier())
	require.NoError(t, err)
	assert.Equal(t, ScopeAPI, result.Scope)
	assert.NotEqual(t, ScopeConnector, result.Scope)
}

func TestExchangeClassifiesErrors(t *testing.T) {
	cases := []struct {
		status     int
		body       string
		wantCode   string
		terminal   bool
		rateLimit  bool
		wantSubstr string
	}{
		{
			status:     http.StatusBadRequest,
			body:       `{"error":"invalid_request","error_description":"code_challenge_method differs"}`,
			wantCode:   "invalid_request",
			terminal:   true,
			wantSubstr: "code_challenge_method differs",
		},
		{
			status: http.StatusForbidden, body: `{"error":"invalid_grant","error_description":"code expired"}`,
			wantCode: "invalid_grant", terminal: true, wantSubstr: "code expired",
		},
		{
			status: http.StatusTooManyRequests, body: `{"error":"rate_limited"}`,
			terminal: false, rateLimit: true, wantSubstr: "too many authorizations",
		},
		{
			status: http.StatusBadGateway, body: "upstream is down",
			terminal: false, wantSubstr: "unexpected response",
		},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			origins := exchangeServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			p, err := NewPKCE()
			require.NoError(t, err)
			_, err = origins.Exchange(t.Context(), nil, "code", p.Verifier())
			require.Error(t, err)

			var xerr *ExchangeError
			require.ErrorAs(t, err, &xerr)
			assert.Equal(t, tc.status, xerr.Status)
			assert.Equal(t, tc.terminal, xerr.Terminal())
			assert.Equal(t, tc.rateLimit, xerr.RateLimited())
			assert.Contains(t, err.Error(), tc.wantSubstr)
			// The verifier must never reach an error message.
			assert.NotContains(t, err.Error(), p.Verifier())
		})
	}
}

func TestExchangeMissingInputsFailBeforeNetwork(t *testing.T) {
	origins := exchangeServer(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request should be sent for a missing code or verifier")
	})
	_, err := origins.Exchange(t.Context(), nil, "", "verifier")
	require.Error(t, err)
	_, err = origins.Exchange(t.Context(), nil, "code", "")
	require.Error(t, err)
}

func TestExchangeRejectsResponseWithoutKey(t *testing.T) {
	origins := exchangeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"user_id":"u1","scope":"api"}`))
	})
	p, err := NewPKCE()
	require.NoError(t, err)
	_, err = origins.Exchange(t.Context(), nil, "code", p.Verifier())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no key")
}

// A proxy that echoes the request must not be able to leak a key back into an
// error message.
func TestExchangeRedactsKeyShapedText(t *testing.T) {
	origins := exchangeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"denied","error_description":"key ` + fakeKey + ` was rejected"}`))
	})
	p, err := NewPKCE()
	require.NoError(t, err)
	_, err = origins.Exchange(t.Context(), nil, "code", p.Verifier())
	require.Error(t, err)
	assert.NotContains(t, err.Error(), fakeKey)
	assert.Contains(t, err.Error(), "[redacted]")
}

func TestExchangeNetworkFailureIsActionable(t *testing.T) {
	origins := testOrigins(t, "http://127.0.0.1:1", "http://127.0.0.1:1")
	p, err := NewPKCE()
	require.NoError(t, err)
	_, err = origins.Exchange(t.Context(), nil, "code", p.Verifier())
	require.Error(t, err)
	var xerr *ExchangeError
	require.ErrorAs(t, err, &xerr)
	assert.Equal(t, 0, xerr.Status)
	assert.Contains(t, err.Error(), "could not reach")
}

func TestExchangeRejectsOversizedResponse(t *testing.T) {
	origins := exchangeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"key":"` + strings.Repeat("a", int(exchangeBodyLimit)+100) + `"}`))
	})
	p, err := NewPKCE()
	require.NoError(t, err)
	_, err = origins.Exchange(t.Context(), nil, "code", p.Verifier())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too large")
}

// ---------------------------------------------------------------------------
// Connect state machine: every terminal path releases the lock.
// ---------------------------------------------------------------------------

func connectWithServer(t *testing.T, handler http.HandlerFunc) (*Connect, *Store) {
	t.Helper()
	origins := exchangeServer(t, handler)
	store, _ := newTestStore(t)
	return NewConnect(origins, store, nil), store
}

func okExchange(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"key":"` + fakeKey + `","user_id":"u1","scope":"api"}`))
}

func TestConnectSuccessPersistsCredential(t *testing.T) {
	conn, store := connectWithServer(t, okExchange)
	var opened string
	cred, err := conn.Run(t.Context(), ConnectOptions{
		OpenBrowser: func(u string) { opened = u },
		AwaitCode:   func(context.Context) (string, error) { return "code-1", nil },
	})
	require.NoError(t, err)
	assert.Equal(t, fakeKey, cred.APIKey)
	assert.Equal(t, llm.CredentialSourcePKCE, cred.Source)
	assert.Equal(t, StateSucceeded, conn.Status().State)
	assert.False(t, conn.Busy(), "the login lock must be released on success")

	// The credential came out of the shared store, not the connect object.
	stored, err := store.Credential(t.Context())
	require.NoError(t, err)
	assert.Equal(t, fakeKey, stored.APIKey)

	assert.Contains(t, opened, AuthorizePath)
	assert.Contains(t, opened, "code_challenge_method=S256")
	assert.NotContains(t, opened, "code_verifier")
}

func TestConnectDenialReleasesLock(t *testing.T) {
	conn, _ := connectWithServer(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("a denial must not reach the exchange endpoint")
	})
	_, err := conn.Run(t.Context(), ConnectOptions{
		AwaitCode: func(context.Context) (string, error) { return "", errors.New("user denied") },
	})
	require.Error(t, err)
	assert.False(t, conn.Busy())
	assert.Equal(t, StateFailed, conn.Status().State)
	assert.NotEmpty(t, conn.Status().Hint, "the user needs an actionable hint")
}

func TestConnectTimeoutReleasesLock(t *testing.T) {
	conn, _ := connectWithServer(t, okExchange)
	_, err := conn.Run(t.Context(), ConnectOptions{
		Timeout: 20 * time.Millisecond,
		AwaitCode: func(ctx context.Context) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
	})
	require.Error(t, err)
	assert.False(t, conn.Busy())
	assert.Equal(t, StateTimedOut, conn.Status().State)
}

func TestConnectExplicitCancelReleasesLock(t *testing.T) {
	conn, _ := connectWithServer(t, okExchange)
	_, err := conn.Run(t.Context(), ConnectOptions{
		AwaitCode: func(ctx context.Context) (string, error) {
			conn.Cancel()
			<-ctx.Done()
			return "", ctx.Err()
		},
	})
	require.Error(t, err)
	assert.False(t, conn.Busy())
	assert.Equal(t, StateCanceled, conn.Status().State)
}

func TestConnectExchangeErrorReleasesLock(t *testing.T) {
	conn, _ := connectWithServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"code already used"}`))
	})
	_, err := conn.Run(t.Context(), ConnectOptions{
		AwaitCode: func(context.Context) (string, error) { return "used-code", nil },
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "code already used")
	assert.False(t, conn.Busy())
	assert.Equal(t, StateFailed, conn.Status().State)
}

func TestConnectRejectedCodeIsTerminal(t *testing.T) {
	conn, _ := connectWithServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	})
	_, err := conn.Run(t.Context(), ConnectOptions{
		AwaitCode: func(context.Context) (string, error) { return "expired", nil },
	})
	require.Error(t, err)
	var xerr *ExchangeError
	require.ErrorAs(t, err, &xerr)
	assert.True(t, xerr.Terminal(), "an expired or reused code must not be retried")
	assert.False(t, xerr.RateLimited())
}

func TestConnectRateLimitIsNotTerminal(t *testing.T) {
	conn, _ := connectWithServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate_limited"}`))
	})
	_, err := conn.Run(t.Context(), ConnectOptions{
		AwaitCode: func(context.Context) (string, error) { return "code", nil },
	})
	require.Error(t, err)
	var xerr *ExchangeError
	require.ErrorAs(t, err, &xerr)
	assert.True(t, xerr.RateLimited())
	assert.False(t, xerr.Terminal(), "the code stays valid until its TTL; the user may retry")
}

func TestConnectRejectsUnexpectedGrantedScope(t *testing.T) {
	conn, store := connectWithServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"key":"` + fakeKey + `","user_id":"u1","scope":"admin"}`))
	})
	_, err := conn.Run(t.Context(), ConnectOptions{
		AwaitCode: func(context.Context) (string, error) { return "code", nil },
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "admin")
	assert.False(t, conn.Busy())
	_, credErr := store.Credential(t.Context())
	assert.ErrorIs(t, credErr, ErrNoCredential, "an unusable grant must not be stored")
}

// A stale response must never overwrite a newer login generation.
func TestConnectStaleAttemptCannotOverrideNewerLogin(t *testing.T) {
	release := make(chan struct{})
	conn, store := connectWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		if body["code"] == "old-code" {
			<-release // hold the first exchange open
		}
		_, _ = w.Write([]byte(`{"key":"` + fakeKey + `","user_id":"` + body["code"] + `","scope":"api"}`))
	})

	first, err := conn.Begin(ConnectOptions{})
	require.NoError(t, err)

	// A second attempt supersedes the first while its exchange is in flight.
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = conn.SubmitCode(t.Context(), first.Attempt, "old-code")
	})

	second, err := conn.Begin(ConnectOptions{})
	require.NoError(t, err)
	assert.Greater(t, second.Attempt, first.Attempt)

	_, err = conn.SubmitCode(t.Context(), second.Attempt, "new-code")
	require.NoError(t, err)

	close(release)
	wg.Wait()

	st := conn.Status()
	assert.Equal(t, StateSucceeded, st.State)
	assert.Equal(t, second.Attempt, st.Attempt, "the newer attempt must own the state")
	assert.False(t, st.State.Busy())

	cred, err := store.Credential(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "new-code", cred.AccountID, "the newer login's credential must be the current one")
}

func TestConnectAbandonClearsStateWithoutError(t *testing.T) {
	conn, _ := connectWithServer(t, okExchange)
	status, err := conn.Begin(ConnectOptions{})
	require.NoError(t, err)
	require.True(t, conn.Busy())

	require.True(t, conn.Abandon(status.Attempt))
	assert.False(t, conn.Busy(), "pagehide must release the lock")
	final := conn.Status()
	assert.Empty(t, final.Error, "a pagehide is not a user-visible failure")
	assert.Empty(t, final.Hint)
	assert.Equal(t, StateCanceled, final.State)

	// A second login can start without any remount.
	second, err := conn.Begin(ConnectOptions{})
	require.NoError(t, err)
	assert.Greater(t, second.Attempt, status.Attempt)
	assert.True(t, conn.Busy())
}

func TestConnectAbandonWithStaleAttemptIsIgnored(t *testing.T) {
	conn, _ := connectWithServer(t, okExchange)
	first, err := conn.Begin(ConnectOptions{})
	require.NoError(t, err)
	second, err := conn.Begin(ConnectOptions{})
	require.NoError(t, err)

	assert.False(t, conn.Abandon(first.Attempt), "a stale pagehide must not cancel a newer login")
	assert.True(t, conn.Busy())
	assert.Equal(t, second.Attempt, conn.Status().Attempt)
}

func TestConnectCancelAttemptOnlyAffectsCurrentGeneration(t *testing.T) {
	conn, _ := connectWithServer(t, okExchange)
	first, err := conn.Begin(ConnectOptions{})
	require.NoError(t, err)
	_, err = conn.Begin(ConnectOptions{})
	require.NoError(t, err)

	assert.False(t, conn.CancelAttempt(first.Attempt))
	assert.True(t, conn.Busy())
}

// Flow A: state is compared before the code is used.
func TestCallbackQueryStateMismatchFailsClosed(t *testing.T) {
	conn, _ := connectWithServer(t, okExchange)
	status, err := conn.Begin(ConnectOptions{CallbackURL: "http://127.0.0.1:1234/cb"})
	require.NoError(t, err)

	_, err = conn.CallbackQuery(status.Attempt, map[string][]string{
		"code":  {"attacker-code"},
		"state": {"not-the-state"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "state mismatch")
	assert.Equal(t, StateFailed, conn.Status().State)
	assert.False(t, conn.Busy())
}

func TestCallbackQueryAcceptsMatchingState(t *testing.T) {
	conn, _ := connectWithServer(t, okExchange)
	status, err := conn.Begin(ConnectOptions{CallbackURL: "http://127.0.0.1:1234/cb"})
	require.NoError(t, err)

	// Recover the state the attempt sent by parsing the authorize URL.
	u, err := url.Parse(status.AuthorizeURL)
	require.NoError(t, err)
	code, err := conn.CallbackQuery(status.Attempt, map[string][]string{
		"code":  {"good-code"},
		"state": {u.Query().Get("state")},
	})
	require.NoError(t, err)
	assert.Equal(t, "good-code", code)
}

func TestCallbackQueryDenialIsReported(t *testing.T) {
	conn, _ := connectWithServer(t, okExchange)
	status, err := conn.Begin(ConnectOptions{CallbackURL: "http://127.0.0.1:1234/cb"})
	require.NoError(t, err)
	u, err := url.Parse(status.AuthorizeURL)
	require.NoError(t, err)

	_, err = conn.CallbackQuery(status.Attempt, map[string][]string{
		"error": {"access_denied"},
		"state": {u.Query().Get("state")},
	})
	require.Error(t, err)
	assert.Equal(t, StateDenied, conn.Status().State)
	assert.False(t, conn.Busy())
}

func TestCallbackQueryRejectsSupersededAttempt(t *testing.T) {
	conn, _ := connectWithServer(t, okExchange)
	first, err := conn.Begin(ConnectOptions{CallbackURL: "http://127.0.0.1:1234/cb"})
	require.NoError(t, err)
	_, err = conn.Begin(ConnectOptions{})
	require.NoError(t, err)

	_, err = conn.CallbackQuery(first.Attempt, map[string][]string{"code": {"c"}, "state": {"s"}})
	require.Error(t, err)
}

func TestConnectSecondAttemptGetsFreshPKCE(t *testing.T) {
	conn, _ := connectWithServer(t, okExchange)
	first, err := conn.Begin(ConnectOptions{})
	require.NoError(t, err)
	second, err := conn.Begin(ConnectOptions{})
	require.NoError(t, err)

	u1, err := url.Parse(first.AuthorizeURL)
	require.NoError(t, err)
	u2, err := url.Parse(second.AuthorizeURL)
	require.NoError(t, err)
	assert.NotEqual(t, u1.Query().Get("code_challenge"), u2.Query().Get("code_challenge"))
	assert.NotEqual(t, u1.Query().Get("state"), u2.Query().Get("state"))
}

func TestConnectStatusNeverCarriesTheVerifier(t *testing.T) {
	conn, _ := connectWithServer(t, okExchange)
	status, err := conn.Begin(ConnectOptions{})
	require.NoError(t, err)

	blob, err := json.Marshal(status)
	require.NoError(t, err)
	assert.NotContains(t, string(blob), "code_verifier")
	assert.NotContains(t, string(blob), fakeKey)
}

// ---------------------------------------------------------------------------
// Loopback callback listener (Flow A).
// ---------------------------------------------------------------------------

func TestCallbackListenerDeliversCode(t *testing.T) {
	conn, _ := connectWithServer(t, okExchange)
	status, err := conn.Begin(ConnectOptions{CallbackURL: "http://127.0.0.1:0/cb"})
	require.NoError(t, err)

	listener, err := ListenForCallback(conn)
	require.NoError(t, err)
	require.Positive(t, listener.Port())
	assert.Contains(t, listener.CallbackURL(), "127.0.0.1")

	u, err := url.Parse(status.AuthorizeURL)
	require.NoError(t, err)
	state := u.Query().Get("state")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan string, 1)
	go func() {
		code, waitErr := listener.Wait(ctx)
		if waitErr == nil {
			done <- code
		}
	}()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		listener.CallbackURL()+"?code=loopback-code&state="+url.QueryEscape(state), http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	select {
	case code := <-done:
		assert.Equal(t, "loopback-code", code)
	case <-ctx.Done():
		t.Fatal("the listener never resolved")
	}
}

func TestCallbackListenerClosesOnCancel(t *testing.T) {
	conn, _ := connectWithServer(t, okExchange)
	_, err := conn.Begin(ConnectOptions{CallbackURL: "http://127.0.0.1:0/cb"})
	require.NoError(t, err)
	listener, err := ListenForCallback(conn)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = listener.Wait(ctx)
	require.Error(t, err)

	// The port must be released, so a second listener can bind.
	second, err := ListenForCallback(conn)
	require.NoError(t, err)
	second.Close()
}

// ---------------------------------------------------------------------------
// Origin separation and credential lifetime.
// ---------------------------------------------------------------------------

// The auth origin and the inference origin are distinct and must never be
// confused: the exchange belongs on the auth origin, the catalog on the
// inference origin. This drives both against separate servers and asserts each
// one was reached, and only it.
func TestAuthAndInferenceOriginsStaySeparate(t *testing.T) {
	var authHits, apiHits atomic.Int64
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHits.Add(1)
		assert.Equal(t, ExchangePath, r.URL.Path,
			"the auth origin must only see the exchange path")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":"` + fakeKey + `","user_id":"u1","scope":"api"}`))
	}))
	t.Cleanup(authServer.Close)
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiHits.Add(1)
		assert.Equal(t, ModelsPath, r.URL.Path,
			"the inference origin must only see the catalog path")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"openai/gpt-5.5","supported_endpoint_types":["openai"]}]}`))
	}))
	t.Cleanup(apiServer.Close)

	origins := testOrigins(t, authServer.URL, apiServer.URL)
	assert.Equal(t, authServer.URL+ExchangePath, origins.ExchangeURL())
	assert.Equal(t, apiServer.URL+ModelsPath, origins.ModelsURL())
	assert.Equal(t, apiServer.URL+"/v1", origins.InferenceBaseURL())

	store, _ := newTestStore(t)
	conn := NewConnect(origins, store, nil)
	_, err := conn.Run(t.Context(), ConnectOptions{
		AwaitCode: func(context.Context) (string, error) { return "code-1", nil },
	})
	require.NoError(t, err)
	assert.EqualValues(t, 1, authHits.Load(), "the exchange must reach the auth origin")

	catalog := origins.LiveCatalogWithFallback(t.Context(), nil, fakeKey, CapabilityChat)
	require.False(t, catalog.Degraded)
	assert.EqualValues(t, 1, apiHits.Load(), "the catalog must reach the inference origin")
	assert.Zero(t, authHits.Load()-1, "discovery must not touch the auth origin")
}

// A rejected key is terminal: there is no refresh grant, so nothing may be
// retried and a refresh_token in the response must not be stored or acted on.
func TestRevokedKeyDoesNotTriggerARefresh(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_api_key","refresh_token":"rt-should-be-ignored"}`))
	}))
	t.Cleanup(server.Close)

	origins := testOrigins(t, server.URL, server.URL)
	store, _ := newTestStore(t)
	conn := NewConnect(origins, store, nil)
	_, err := conn.Run(t.Context(), ConnectOptions{
		AwaitCode: func(context.Context) (string, error) { return "code", nil },
	})
	require.Error(t, err)
	assert.EqualValues(t, 1, calls.Load(), "a terminal 401 must not be retried")

	// The relay's refresh_token is not a credential this client understands.
	_, credErr := store.Credential(t.Context())
	assert.ErrorIs(t, credErr, ErrNoCredential)
	assert.NotContains(t, err.Error(), "rt-should-be-ignored")
}
