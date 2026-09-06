package project

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestThinkingPreferencePersistsWithoutLosingConfig(t *testing.T) {
	global := GlobalLayout{root: t.TempDir()}
	original := `# keep my model configuration
models:
  - name: test
    api_key: secret
tree: {filter: user-only}
tui: {other_setting: keep, thinking_expanded: false} # keep this comment
unknown: preserved
`
	require.NoError(t, os.WriteFile(global.ConfigFile(), []byte(original), 0o600))
	for _, expanded := range []bool{true, false} {
		require.NoError(t, SetThinkingExpanded(global, expanded))
		cfg, err := parseConfigFile(global.ConfigFile())
		require.NoError(t, err)
		assert.Equal(t, expanded, cfg.TUI.ThinkingExpanded)
		assert.Equal(t, "secret", cfg.Models[0].APIKey)
		assert.Equal(t, "user-only", cfg.Tree.Filter)
		data, err := os.ReadFile(global.ConfigFile())
		require.NoError(t, err)
		assert.Contains(t, string(data), "# keep my model configuration")
		assert.Contains(t, string(data), "# keep this comment")
		var document map[string]any
		require.NoError(t, yaml.Unmarshal(data, &document))
		assert.Equal(t, "preserved", document["unknown"])
		assert.Equal(t, "keep", document["tui"].(map[string]any)["other_setting"])
	}
}

func TestThinkingPreferenceDefaultAndMissingSection(t *testing.T) {
	for _, content := range []string{"", "models: []\n", "tui: null\n", "null\n"} {
		t.Run(content, func(t *testing.T) {
			global := GlobalLayout{root: t.TempDir()}
			if content != "" {
				require.NoError(t, os.WriteFile(global.ConfigFile(), []byte(content), 0o600))
			}
			cfg, err := parseConfigFile(global.ConfigFile())
			require.NoError(t, err)
			assert.False(t, cfg.TUI.ThinkingExpanded)
			require.NoError(t, SetThinkingExpanded(global, true))
			cfg, err = parseConfigFile(global.ConfigFile())
			require.NoError(t, err)
			assert.True(t, cfg.TUI.ThinkingExpanded)
		})
	}
}

func TestThinkingPreferenceRejectsMalformedConfigWithoutWriting(t *testing.T) {
	for _, content := range []string{"tui: [", "tui: {thinking_expanded: invalid}", "tui: []", "tui: {}\ntui: {}"} {
		t.Run(content, func(t *testing.T) {
			global := GlobalLayout{root: t.TempDir()}
			require.NoError(t, os.WriteFile(global.ConfigFile(), []byte(content), 0o600))
			require.Error(t, SetThinkingExpanded(global, true))
			data, err := os.ReadFile(global.ConfigFile())
			require.NoError(t, err)
			assert.Equal(t, content, string(data))
		})
	}
}
