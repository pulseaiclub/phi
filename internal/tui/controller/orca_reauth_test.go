package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/project"
)

// orcaTestKey is fake. No test may use a real key.
const orcaTestKey = "sk-orca-test-only-not-a-real-key"

func newOrcaController(t *testing.T) (*EngineController, *project.Project) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PHI_MODEL", "openai/gpt-5.5")
	t.Setenv("PHI_API_KEY", "")
	t.Setenv("ORCA_API_KEY", "")

	cwd := t.TempDir()
	proj, err := project.Discover(cwd)
	require.NoError(t, err)
	require.NoError(t, proj.LoadConfig())

	return &EngineController{bus: NewBus(nil), proj: proj, cwd: cwd}, proj
}

// A non-OrcaRouter model must not get a reauthentication callback: the
// callback exists because an OrcaRouter key is durable, and applying it to
// another provider would mark the wrong credential.
func TestOrcaAuthFailureOnlyForOrcaRouter(t *testing.T) {
	ctrl, _ := newOrcaController(t)
	assert.Nil(t, ctrl.orcaAuthFailure(llm.ModelConfig{API: llm.OpenAI, Name: "gpt-4o"}))
	assert.Nil(t, ctrl.orcaAuthFailure(llm.ModelConfig{Name: "plain-model"}))

	fn := ctrl.orcaAuthFailure(llm.ModelConfig{API: llm.OrcaRouter, Name: "openai/gpt-5.5"})
	require.NotNil(t, fn, "an OrcaRouter model needs the terminal-401 callback")
}

// A 401 must mark the credential that actually made the request, and a stale
// failure from an older generation must not poison a newer login.
func TestOrcaAuthFailureMarksOnlyTheRejectedGeneration(t *testing.T) {
	ctrl, proj := newOrcaController(t)
	store := proj.OrcaStore()

	_, err := store.SetAPIKey(orcaTestKey, "user-1")
	require.NoError(t, err)

	fn := ctrl.orcaAuthFailure(llm.ModelConfig{API: llm.OrcaRouter, Name: "openai/gpt-5.5"})
	require.NotNil(t, fn)

	// A newer login lands before the old 401 arrives.
	_, err = store.SetPKCEKey("sk-orca-a-newer-key", "user-1")
	require.NoError(t, err)

	fn(llm.ModelConfig{API: llm.OrcaRouter, Name: "openai/gpt-5.5"})

	status := store.Status(t.Context())
	assert.True(t, status.Present)
	assert.False(t, status.NeedsReauth,
		"a stale 401 must not mark the freshly acquired credential")
	cred, err := store.Credential(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "sk-orca-a-newer-key", cred.APIKey)
}

// The 401 that belongs to the engine's own generation must be recorded, and
// reported once rather than on every later failure.
func TestOrcaAuthFailureMarksTheBoundGenerationOnce(t *testing.T) {
	ctrl, proj := newOrcaController(t)
	store := proj.OrcaStore()
	_, err := store.SetAPIKey(orcaTestKey, "user-1")
	require.NoError(t, err)

	fn := ctrl.orcaAuthFailure(llm.ModelConfig{API: llm.OrcaRouter, Name: "openai/gpt-5.5"})
	require.NotNil(t, fn)
	rejected := llm.ModelConfig{API: llm.OrcaRouter, Name: "openai/gpt-5.5"}

	fn(rejected)
	status := store.Status(t.Context())
	assert.True(t, status.NeedsReauth, "the rejected generation must be marked")
	assert.Equal(t, "user-1", status.AccountID,
		"the exact account that was rejected must be recorded")

	// A second 401 from the same generation is a no-op: the user is told once.
	assert.False(t, store.MarkUnauthorized(store.Generation()))
}

func TestOrcaAuthFailureIgnoresNonOrcaRejectedConfig(t *testing.T) {
	ctrl, proj := newOrcaController(t)
	store := proj.OrcaStore()
	_, err := store.SetAPIKey(orcaTestKey, "")
	require.NoError(t, err)

	fn := ctrl.orcaAuthFailure(llm.ModelConfig{API: llm.OrcaRouter, Name: "openai/gpt-5.5"})
	require.NotNil(t, fn)

	// The engine reports the model config that failed; a non-OrcaRouter one is
	// not ours to mark.
	fn(llm.ModelConfig{API: llm.Anthropic, Name: "claude-sonnet-4-20250514"})
	assert.False(t, store.Status(t.Context()).NeedsReauth)
}

// The stored secret survives a reauthentication mark: the user may be offline
// and unable to obtain a new one, so nothing is destroyed automatically.
func TestOrcaAuthFailureKeepsTheStoredSecret(t *testing.T) {
	ctrl, proj := newOrcaController(t)
	store := proj.OrcaStore()
	_, err := store.SetAPIKey(orcaTestKey, "")
	require.NoError(t, err)

	fn := ctrl.orcaAuthFailure(llm.ModelConfig{API: llm.OrcaRouter, Name: "openai/gpt-5.5"})
	require.NotNil(t, fn)
	fn(llm.ModelConfig{API: llm.OrcaRouter, Name: "openai/gpt-5.5"})

	status := store.Status(t.Context())
	assert.True(t, status.NeedsReauth)
	assert.True(t, status.Present, "the secret is kept, only flagged")
	cred, err := store.Credential(t.Context())
	require.NoError(t, err)
	assert.Equal(t, orcaTestKey, cred.APIKey)

	// The user is told how to recover, without the key appearing in the text.
	messages := ctrl.bus.Drain()
	require.NotEmpty(t, messages)
	found := false
	for _, m := range messages {
		toastMsg, ok := m.(ToastMsg)
		if !ok {
			continue
		}
		found = true
		assert.Contains(t, toastMsg.Message, "phi auth login --orcarouter")
		assert.NotContains(t, toastMsg.Message, orcaTestKey)
	}
	assert.True(t, found, "the user needs an actionable notification")
}
