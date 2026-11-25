package lib

import (
	"errors"
	"testing"

	"github.com/pulseaiclub/lug"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

type stubRegistry struct {
	registered []Provider
	err        error
}

func (s *stubRegistry) Register(provider Provider) error {
	if s.err != nil {
		return s.err
	}
	s.registered = append(s.registered, provider)
	return nil
}

const providerTable = `local p = { id = "acme", api = "OpenAI", base_url = "https://api.acme.example/v1",
  models = { { id = "chat", context_window = 128000, max_tokens = 8192 } } }; `

func TestProviderBinding(t *testing.T) {
	registry := &stubRegistry{}
	env := lug.New()
	NewProviderBinding(registry).Bind(env)

	_, err := lug.DoString(env, providerTable+"phi.provider.register(p)")
	require.NoError(t, err)
	require.Len(t, registry.registered, 1)
	assert.Equal(t, Provider{
		ID: "acme", API: llm.OpenAI, BaseURL: "https://api.acme.example/v1",
		Models: []ProviderModel{{ID: "chat", ContextWindow: 128000, MaxTokens: 8192}},
	}, registry.registered[0])

	t.Run("parse error is prefixed", func(t *testing.T) {
		_, err := lug.DoString(env, providerTable+`p.id = "a/b"; phi.provider.register(p)`)
		require.ErrorContains(t, err, "phi.provider.register")
		assert.Len(t, registry.registered, 1)
	})
	t.Run("registry error is prefixed", func(t *testing.T) {
		registry.err = errors.New("duplicate provider")
		defer func() { registry.err = nil }()
		_, err := lug.DoString(env, providerTable+"phi.provider.register(p)")
		require.ErrorContains(t, err, "phi.provider.register: duplicate provider")
	})
}
