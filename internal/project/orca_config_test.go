package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/orca"
	"github.com/pulseaiclub/phi/internal/project/model"
)

// orcaTestKey is fake. Tests must never use a real key.
const orcaTestKey = "sk-orca-test-only-not-a-real-key"

func writeOrcaConfig(t *testing.T, p *Project, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(p.Global().Root(), 0o755))
	require.NoError(t, os.WriteFile(p.Global().ConfigFile(), []byte(body), 0o644))
}

// Both authentication adapters must reach the provider through one seam: the
// request path reads the same store either way and cannot tell them apart.
func TestOrcaCredentialResolvesIntoConfigFromEitherAdapter(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source llm.CredentialSource
		store  func(*orca.Store) error
	}{
		{
			name:   "api key adapter",
			source: llm.CredentialSourceAPIKey,
			store: func(s *orca.Store) error {
				_, err := s.SetAPIKey(orcaTestKey, "")
				return err
			},
		},
		{
			name:   "pkce adapter",
			source: llm.CredentialSourcePKCE,
			store: func(s *orca.Store) error {
				_, err := s.SetPKCEKey(orcaTestKey, "user-1")
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := discoverInTempHome(t)
			t.Setenv(orca.EnvAPIKey, "")
			require.NoError(t, tc.store(p.OrcaStore()))

			// An OrcaRouter entry with no api_key in the file is valid: the
			// credential comes from the store.
			writeOrcaConfig(t, p, "models:\n  - name: openai/gpt-5.5\n    api: OrcaRouter\n    default: true\n")
			require.NoError(t, p.LoadConfig())

			cfg := p.Config()
			require.Len(t, cfg.Models, 1)
			assert.Equal(t, llm.OrcaRouter, cfg.Models[0].API)
			assert.Equal(t, orcaTestKey, cfg.Models[0].APIKey,
				"the request path must receive the credential regardless of adapter")
			assert.Equal(t, model.OrcaRouterBaseURL, cfg.Models[0].BaseURL)
			assert.True(t, cfg.UsesOrcaRouter())

			status := cfg.OrcaCredentialStatus()
			assert.True(t, status.Present)
			assert.Equal(t, tc.source, status.Source)
			assert.NotEqual(t, orcaTestKey, status.Masked, "status must be redacted")
			assert.NotContains(t, status.Masked, orcaTestKey)
		})
	}
}

func TestOrcaConfigWithoutCredentialStillLoads(t *testing.T) {
	p := discoverInTempHome(t)
	t.Setenv(orca.EnvAPIKey, "")
	writeOrcaConfig(t, p, "models:\n  - name: openai/gpt-5.5\n    api: OrcaRouter\n    default: true\n")
	require.NoError(t, p.LoadConfig(), "a missing credential is a request-time error, not a parse error")

	cfg := p.Config()
	assert.Empty(t, cfg.Models[0].APIKey)
	assert.False(t, cfg.OrcaCredentialStatus().Present)
}

// A non-OrcaRouter model still requires a key: relaxing that would turn a
// typo in `api:` into a silent unauthenticated request.
func TestNonOrcaConfigStillRequiresAPIKey(t *testing.T) {
	p := discoverInTempHome(t)
	t.Setenv(orca.EnvAPIKey, "")
	writeOrcaConfig(t, p, "models:\n  - name: gpt-4o\n    default: true\n")
	err := p.LoadConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "api_key")
}

func TestOrcaEnvKeyOverridesStore(t *testing.T) {
	p := discoverInTempHome(t)
	_, err := p.OrcaStore().SetAPIKey(orcaTestKey, "")
	require.NoError(t, err)

	t.Setenv(orca.EnvAPIKey, "sk-orca-from-environment")
	writeOrcaConfig(t, p, "models:\n  - name: openai/gpt-5.5\n    api: OrcaRouter\n    default: true\n")
	require.NoError(t, p.LoadConfig())
	assert.Equal(t, "sk-orca-from-environment", p.Config().Models[0].APIKey,
		"the project's existing env-secret habit must keep working")
}

// The credential file must not be world-readable: it holds a bearer key.
func TestOrcaStoreFilePermissions(t *testing.T) {
	p := discoverInTempHome(t)
	_, err := p.OrcaStore().SetAPIKey(orcaTestKey, "")
	require.NoError(t, err)

	path := orca.DefaultStorePath(p.Global().Root())
	require.NotEmpty(t, path)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	assert.Equal(t, filepath.Join(p.Global().Root(), "orcarouter.json"), path)

	// The key is on disk (it must persist) but never in a second location.
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), orcaTestKey)
}

func TestOrcaStoreSurvivesReload(t *testing.T) {
	p := discoverInTempHome(t)
	_, err := p.OrcaStore().SetAPIKey(orcaTestKey, "")
	require.NoError(t, err)

	// A new Project over the same home sees the same credential: a restart
	// must not force a second authorization.
	t.Setenv(orca.EnvAPIKey, "")
	again, err := Discover("")
	require.NoError(t, err)
	cred, err := again.OrcaStore().Credential(t.Context())
	require.NoError(t, err)
	assert.Equal(t, orcaTestKey, cred.APIKey)
	assert.Equal(t, llm.CredentialSourceAPIKey, cred.Source)
}
