package plugin

import (
	"testing"

	"github.com/pulseaiclub/lug"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/plugin/lib"

	"github.com/pulseaiclub/phi/internal/llm"
)

func luaDeclaration(t *testing.T, source string) lug.Value {
	t.Helper()
	values, err := lug.DoString(lug.New(), source)
	require.NoError(t, err)
	require.Len(t, values, 1)
	return values[0]
}

const pkceTable = `local p = { client_id = "phi-cli", authorize_url = "https://auth.acme.example/authorize",
  token_url = "https://auth.acme.example/token", scopes = { "chat" } }; `

func TestParsePKCE(t *testing.T) {
	p, err := lib.ParsePKCE(luaDeclaration(t, pkceTable+"return p"))
	require.NoError(t, err)
	assert.Equal(t, lib.PKCE{
		ClientID: "phi-cli", AuthorizeURL: "https://auth.acme.example/authorize",
		TokenURL: "https://auth.acme.example/token", Scopes: []string{"chat"},
	}, p)

	for _, tc := range []struct{ change, field string }{
		{"p = 42", "pkce"},
		{"p.client_id = nil", "client_id"},
		{"p.client_id = false", "client_id"},
		{`p.authorize_url = "/authorize"`, "authorize_url"},
		{`p.token_url = "ftp://example.org/token"`, "token_url"},
		{`p.token_url = "https://user@host/token"`, "token_url"},
		{`p.authorize_url = "https://host/path#fragment"`, "authorize_url"},
		{`p.scopes = "chat"`, "scopes"},
		{`p.scopes[3] = "write"`, "scopes"},
		{`p.scopes.extra = "write"`, "scopes"},
		{`p.scopes[1] = 4`, "scopes[1]"},
		{`p.scopes[1] = " "`, "scopes[1]"},
		{`p.secret = true`, "secret"},
	} {
		t.Run(tc.change, func(t *testing.T) {
			_, err := lib.ParsePKCE(luaDeclaration(t, pkceTable+tc.change+"; return p"))
			require.ErrorContains(t, err, tc.field)
		})
	}
}

const providerTable = `local p = { id = "acme", api = "OpenAI", base_url = "https://api.acme.example/v1",
  models = { { id = "chat", context_window = 128000, max_tokens = 8192 } } }; `

func TestParseProvider(t *testing.T) {
	v := luaDeclaration(t, providerTable+"return p")
	auth, err := lib.ParsePKCE(luaDeclaration(t, pkceTable+"return p"))
	require.NoError(t, err)
	v.Table().Set(lug.String("auth"), lug.ValueOf(&auth))
	got, err := lib.ParseProvider(v)
	require.NoError(t, err)
	assert.Equal(t, lib.Provider{
		ID: "acme", API: llm.OpenAI, BaseURL: "https://api.acme.example/v1",
		Models: []lib.ProviderModel{{ID: "chat", ContextWindow: 128000, MaxTokens: 8192}}, Auth: &auth,
	}, got)
	auth.Scopes[0] = "changed"
	auth.TokenURL = "invalid"
	assert.Equal(t, []string{"chat"}, got.Auth.Scopes)
	assert.Equal(t, "https://auth.acme.example/token", got.Auth.TokenURL)
	_, err = lib.ParseProvider(v)
	require.ErrorContains(t, err, "token_url")

	for _, api := range []llm.RouterType{llm.OpenAI, llm.OpenAIResponses, llm.Anthropic, llm.Gemini} {
		t.Run(string(api), func(t *testing.T) {
			v := luaDeclaration(
				t,
				providerTable+`p.models[2] = { id = "Up//Stream/v1", context_window = 8, max_tokens = 8 }; return p`,
			)
			v.Table().Set(lug.String("api"), lug.String(string(api)))
			p, err := lib.ParseProvider(v)
			require.NoError(t, err)
			assert.Equal(t, api, p.API)
			assert.Nil(t, p.Auth)
			assert.Equal(t, "Up//Stream/v1", p.Models[1].ID)
		})
	}
}

func TestParseProviderInvalid(t *testing.T) {
	for _, tc := range []struct{ change, field string }{
		{`p = nil`, "provider"},
		{`p.id = false`, "id"},
		{`p.id = " "`, "id"},
		{`p.id = "a/b"`, "id"},
		{`p.api = "openai"`, "api"},
		{`p.api = true`, "api"},
		{`p.base_url = "https:///missing-host"`, "base_url"},
		{`p.stream = true`, "stream"},
		{`p[1] = true`, "field names"},
		{`p.models = nil`, "models"},
		{`p.models = {}`, "models"},
		{`p.models[1000000000] = p.models[1]`, "models"},
		{`p.models[2] = p.models[1]; p.models[1] = nil`, "models"},
		{`p.models[1.5] = p.models[1]`, "models"},
		{`p.models["2"] = p.models[1]`, "models"},
		{`p.models[2] = p.models[1]`, "duplicate model"},
		{`p.models[1] = "chat"`, "models[1]"},
		{`p.models[1].id = nil`, "id"},
		{`p.models[1].unknown = 4`, "unknown"},
		{`p.models[1].context_window = 0`, "context_window"},
		{`p.models[1].max_tokens = -1`, "max_tokens"},
		{`p.models[1].max_tokens = "8"`, "max_tokens"},
		{`p.models[1].max_tokens = 0.5`, "max_tokens"},
		{`p.models[1].context_window = 2^63`, "context_window"},
		{`p.models[1].context_window = math.huge`, "context_window"},
		{`p.models[1].max_tokens = 128001`, "max_tokens"},
		{`p.auth = {}`, "auth"},
	} {
		t.Run(tc.change, func(t *testing.T) {
			p, err := lib.ParseProvider(luaDeclaration(t, providerTable+tc.change+"; return p"))
			require.ErrorContains(t, err, tc.field)
			assert.Zero(t, p)
		})
	}
	for _, wrong := range []any{(*lib.PKCE)(nil), lib.PKCE{}, new(int)} {
		v := luaDeclaration(t, providerTable+"return p")
		v.Table().Set(lug.String("auth"), lug.ValueOf(wrong))
		_, err := lib.ParseProvider(v)
		require.ErrorContains(t, err, "auth")
	}
}
