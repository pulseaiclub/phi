package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

// The OrcaRouter route is a first-class named provider: a preset must exist,
// it must not be reachable through a hand-typed base URL, and an unknown model
// in the OrcaRouter namespace must still route to OrcaRouter rather than
// leaking the user's OrcaRouter key to a different vendor.
func TestOrcaPresetRoutesToOrcaRouter(t *testing.T) {
	for _, name := range []string{
		"openai/gpt-5.5",
		"anthropic/claude-opus-4.8",
		"google/gemini-3.5-flash",
		"deepseek/deepseek-v4-pro",
		"orcarouter/auto",
		"openai/some-model-added-later",
	} {
		t.Run(name, func(t *testing.T) {
			preset, ok := Lookup(name)
			require.True(t, ok, "%s must resolve to a preset", name)
			assert.Equal(t, llm.OrcaRouter, preset.Config.API)
			assert.Equal(t, OrcaRouterBaseURL, preset.Config.BaseURL)
			assert.Equal(t, name, preset.Config.Name,
				"the vendor/model namespace must be preserved verbatim")
			assert.Empty(t, preset.Config.APIKey, "a preset never carries a key")
		})
	}
}

func TestOrcaPresetIsNotAFallbackForOtherModels(t *testing.T) {
	// A plain model name is not in the OrcaRouter namespace and must not be
	// silently rerouted: that would send it to a host that rejects the key.
	for _, name := range []string{"gpt-4o", "claude-sonnet-4-20250514", "my-local-llama"} {
		preset, ok := Lookup(name)
		if ok {
			assert.NotEqual(t, llm.OrcaRouter, preset.Config.API,
				"%s must not be rerouted to OrcaRouter", name)
		}
	}
}

func TestOrcaPresetKeepsVerifiedMetadata(t *testing.T) {
	preset, ok := Lookup("openai/gpt-5.5")
	require.True(t, ok)
	assert.Equal(t, 272000, preset.Config.ContextWindow)
	assert.True(t, preset.Config.ImageEnabled)

	// The reasoning ladder is the documented one, in order, and xhigh is not
	// dropped: GPT-5.5 supports low/medium/high/xhigh.
	levels := OrcaReasoningLevels("openai/gpt-5.5")
	require.NotEmpty(t, levels)
	assert.Equal(t, []llm.ThinkMode{llm.Low, llm.Medium, llm.High, llm.XHigh}, levels)
	assert.Equal(t, llm.High, preset.Config.Think.Mode,
		"the documented default for gpt-5.5 is high")
}

func TestOrcaPresetUnknownModelKeepsConservativeDefaults(t *testing.T) {
	preset, ok := Lookup("openai/a-model-that-does-not-exist-yet")
	require.True(t, ok)
	assert.Equal(t, llm.OrcaRouter, preset.Config.API)
	assert.Zero(t, preset.Config.ContextWindow,
		"an unverified context window stays unset so compaction stays off")
	assert.False(t, preset.Config.ImageEnabled,
		"image support is only enabled when the catalog proves it")
	assert.Nil(t, OrcaReasoningLevels("openai/a-model-that-does-not-exist-yet"),
		"a reasoning ladder must never be invented")
}

func TestVerifiedOrcaModelIDsAreNamespaced(t *testing.T) {
	ids := VerifiedOrcaModelIDs()
	require.NotEmpty(t, ids)
	for _, id := range ids {
		assert.Contains(t, id, "/", "%s must keep its vendor namespace", id)
		_, ok := Lookup(id)
		assert.True(t, ok, "%s must be a resolvable preset", id)
	}
	assert.Contains(t, ids, OrcaAutoModel)
}
