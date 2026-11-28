package project

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/core"
)

func TestModelPresetAndOverridesRoundTrip(t *testing.T) {
	for _, tc := range []struct{ name, preset string }{
		{"group/auto-glm-5-3-flash", ""},
		{"opaque-route", "glm-5.3-flash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf(`models:
  - name: %s
    preset: %s
    api: OpenAI
    api_key: test
    base_url: http://127.0.0.1:3425/v1
    context_window: 123456
    image_enabled: false
    think_level: low
    request_parameters:
      thinking:
        clear_thinking: null
      enable_thinking: false
      temperature: 0
`, tc.name, tc.preset)), 0o600))
			doc, err := ReadConfigDoc(path)
			require.NoError(t, err)
			require.NoError(t, doc.Validate())
			assert.Equal(t, core.RequestParameters{
				"thinking": core.RequestParameters{"clear_thinking": nil}, "enable_thinking": false, "temperature": 0,
			}, doc.Models[0].RequestParameters)
			// Saving an unrelated edit must preserve options the editor does not expose.
			doc.Models[0].APIKey = "replacement"
			require.NoError(t, doc.Save(path))
			after, err := ReadConfigDoc(path)
			require.NoError(t, err)
			assert.Equal(t, doc, after)
			cfg, err := parseConfigFile(path)
			require.NoError(t, err)
			m := cfg.Model()
			assert.Equal(t, tc.name, m.Name)
			assert.Equal(t, tc.preset, m.Preset)
			assert.Equal(t, "replacement", m.APIKey)
			assert.Equal(t, "http://127.0.0.1:3425/v1", m.BaseURL)
			assert.Equal(t, core.OpenAI, m.API)
			assert.Equal(t, 123456, m.ContextWindow)
			assert.False(t, m.ImageEnabled)
			assert.True(t, m.Think.Enabled)
			assert.Equal(t, core.Low, m.Think.Mode)
			assert.Equal(t, doc.Models[0].RequestParameters, m.RequestParameters)
		})
	}
}

func TestExplicitPresetBinding(t *testing.T) {
	m, err := ResolveModelDoc(ModelDoc{Name: "opaque-route", Preset: "glm-5.3-flash", API: "OpenAI"})
	require.NoError(t, err)
	assert.Equal(t, "opaque-route", m.Name)
	assert.Equal(t, "glm-5.3-flash", m.Preset)
	assert.True(t, m.ImageEnabled)
	assert.Equal(t, core.Max, m.Think.Mode)
	_, err = ResolveModelDoc(ModelDoc{Name: "opaque-route", Preset: "not-a-preset"})
	require.ErrorContains(t, err, "unknown preset")
}

func TestConfiguredModelWinsOverPHIModel(t *testing.T) {
	p := discoverInTempHome(t)
	require.NoError(t, os.WriteFile(p.Global().ConfigFile(), []byte(`models:
  - name: old-model
    api_key: fake-key
    api: OpenAI
    base_url: https://old.example/v1
`), 0o600))
	t.Setenv("PHI_MODEL", "vendor/glm-5.3-flash")
	t.Setenv("PHI_BASE_URL", "https://new.example/v1")
	t.Setenv("PHI_THINK_LEVEL", "low")
	t.Setenv("PHI_SKILL_PATH", "~/env-skills")
	require.NoError(t, p.LoadConfig())
	m := p.Config().Model()
	assert.Equal(t, "old-model", m.Name)
	assert.Equal(t, "https://new.example/v1", m.BaseURL)
	assert.False(t, m.ImageEnabled)
	assert.Equal(t, core.Low, m.Think.Mode)
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	require.NotEmpty(t, m.SkillPaths)
	assert.Equal(t, filepath.Join(home, "env-skills"), m.SkillPaths[0])
	assert.Contains(t, m.SkillPaths, filepath.Join(p.Root(), ".agents", "skills"))
}

func TestModelThinkingConstraints(t *testing.T) {
	for _, tc := range []struct {
		name, preset, endpoint, level string
		wantError                     bool
	}{
		{"group/auto-glm-5-3-flash", "", "", "medium", true},
		{"opaque", "glm-5.3", "", "off", true},
		{"group/auto-kimi-k3", "", "", "off", true},
		{"vendor/deepseek-flash", "", "", "off", false},
		{"vendor/deepseek-flash", "", "https://opencode.ai/zen/go/v1", "off", true},
		{"opaque", "deepseek-v4-pro", "https://opencode.ai/zen/go/v1/chat/completions", "low", true},
		{"custom", "", "", "medium", false},
		{"custom", "", "", "typo", true},
	} {
		t.Run(tc.name+"/"+tc.level+"/"+tc.endpoint, func(t *testing.T) {
			_, err := ResolveModelDoc(ModelDoc{
				Name: tc.name, Preset: tc.preset, BaseURL: tc.endpoint, API: "OpenAI", ThinkLevel: tc.level,
			})
			if tc.wantError {
				require.ErrorContains(t, err, "think_level")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestFindModelUsesConfigNameUnderPHIModel(t *testing.T) {
	p := discoverInTempHome(t)
	require.NoError(t, os.WriteFile(p.Global().ConfigFile(), []byte(`models:
  - name: local-default
    api_key: local-key
    api: OpenAI
    base_url: https://local.example/v1
  - name: vendor/glm-5.3-flash
    api_key: vendor-key
    api: OpenAI
    base_url: https://vendor.example/v1
`), 0o600))
	t.Setenv("PHI_MODEL", "vendor/glm-5.3-flash")
	require.NoError(t, p.LoadConfig())
	cfg := p.Config()
	require.Equal(t, "local-default", cfg.Model().Name)

	got, ok := cfg.FindModel("local-default")
	require.True(t, ok)
	assert.Equal(t, "local-default", got.Name)
	assert.Equal(t, "https://local.example/v1", got.BaseURL)

	got, ok = cfg.FindModel("vendor/glm-5.3-flash")
	require.True(t, ok)
	assert.Equal(t, "https://vendor.example/v1", got.BaseURL)
}
