package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
)

func TestModelAliasesInheritCapabilitiesWithoutChangingIdentity(t *testing.T) {
	for _, original := range presets {
		for _, name := range []string{
			"vendor/" + original.Config.Name,
			"gateway/vendor/" + original.Config.Name + ":free",
			original.Config.Name + "-20250901",
			original.Config.Name + "-1017",
			"vendor/route/" + original.Config.Name + "-1017:free",
		} {
			t.Run(name, func(t *testing.T) {
				p, ok := Lookup(name)
				require.True(t, ok)
				assert.Equal(t, name, p.Config.Name)
				assert.Equal(t, original.Config.ContextWindow, p.Config.ContextWindow)
				assert.Equal(t, original.Config.ImageEnabled, p.Config.ImageEnabled)
				assert.Equal(t, original.Config.Think, p.Config.Think)
				assert.Empty(t, p.Config.BaseURL, "a routed ID does not select a native connection")
				assert.Empty(t, p.Config.API, "protocol selection belongs to the connection")
			})
		}
	}
}

func TestModelAliasBoundariesAndSpecificity(t *testing.T) {
	for _, name := range []string{
		"notglm-5.3", "glm-5.30", "glm-5.3-unknown", "deepseek-flashy", "group/auto-kimi-k30",
		// A numeric build suffix is four digits exactly; other counts stay unknown.
		"deepseek-v4.1-flash-101", "deepseek-v4.1-flash-10170", "glm-5.3-1017x",
	} {
		_, ok := Lookup(name)
		assert.False(t, ok, name)
	}
	for _, name := range []string{"GLM-5.3-FLASH", "group/auto-glm-5-3-flash", "group/auto-kimi-k3", "gateway/auto-kimi-k3:free"} {
		p, ok := Lookup(name)
		require.True(t, ok, name)
		assert.True(t, p.Config.ImageEnabled, name)
		assert.Equal(t, llm.Max, p.Config.Think.Mode)
	}
	p, ok := Lookup("clinepass/cline-pass/deepseek-v4.1-flash")
	require.True(t, ok)
	assert.True(t, p.Config.ImageEnabled)

	// Routes that pin a build with a four-digit suffix keep the family preset.
	for _, name := range []string{
		"cline/cline-pass/deepseek-v4.1-flash-1017",
		"tokenrhythm/deepseek-v4.1-flash-1017",
	} {
		p, ok := Lookup(name)
		require.True(t, ok, name)
		assert.Equal(t, name, p.Config.Name)
		assert.Equal(t, 384_000, p.Config.MaxTokens, name)
	}
}

func TestModelAliasPrefersModelOverRoutePrefix(t *testing.T) {
	for _, tc := range []struct {
		name, preset string
	}{
		{"vendor/gpt-6-astra/glm-5.3-flash", "glm-5.3-flash"},
		{"vendor/gpt-5.5-pro/glm-5.3", "glm-5.3"},
		{"vendor/glm-5.3-flash/gpt-6-astra", "gpt-6-astra"},
		{"vendor/glm-5.3-flash/auto-kimi-k3:free", "kimi-k3"},
		{"vendor/gpt-5.5-pro/glm-5.3-20250901:free", "glm-5.3"},
		{"vendor/gpt-5.5-pro/glm-5.3/provider", "glm-5.3"},
		{"vendor/glm-5.3:provider/gpt-5.5-pro", "glm-5.3"},
		{"vendor/glm-5.3[provider/gpt-5.5-pro]", "glm-5.3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want, ok := Lookup(tc.preset)
			require.True(t, ok)
			got, ok := Lookup(tc.name)
			require.True(t, ok)
			assert.Equal(t, tc.name, got.Config.Name)
			assert.Equal(t, want.Config.ContextWindow, got.Config.ContextWindow)
			assert.Equal(t, want.Config.ImageEnabled, got.Config.ImageEnabled)
			assert.Equal(t, want.Config.Think, got.Config.Think)
			assert.Equal(t, want.Hooks, got.Hooks)
			assert.Empty(t, got.Config.BaseURL)
			assert.Empty(t, got.Config.API)
		})
	}
}
