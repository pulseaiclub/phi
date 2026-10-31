package controller

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/ext/go/pxb"
	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/extension/create"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/project"
)

const resolveModelExtSrc = `package main

import (
	"context"

	"github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/ext/go/phi"
)

func main() {
	m := phi.New("models", "0.0.1")
	m.RegisterModel(ext.Model{
		Name: "echo", Conn: ext.ConnExt, Preset: "glm-5.3-flash",
		ContextWindow: 1000, ImageEnabled: true, ThinkEnabled: true, ThinkLevel: "high",
		// ConnExt models need a handler even when only resolution is tested.
		Stream: func(context.Context, ext.ModelRequest, func(ext.ModelEvent) error) error {
			return nil
		},
	})
	m.RegisterModel(ext.Model{
		Name: "gw", API: "Anthropic", BaseURL: "http://127.0.0.1:9", APIKey: "k", ContextWindow: 42,
	})
	_ = m.Run()
}
`

func loadResolveModelRunner(t *testing.T) *extension.Runner {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, create.Materialize(
		t.Context(), filepath.Join(root, "models"), "models", "0.0.1", resolveModelExtSrc))
	r, warns, err := extension.Load(root, "")
	require.NoError(t, err)
	require.Empty(t, warns, "%v", warns)
	t.Cleanup(r.Close)
	return r
}

func testConfig() *project.Config {
	return &project.Config{
		Models: []llm.ModelConfig{{
			Name: "primary", APIKey: "sk-p", BaseURL: "http://primary.example/v1", ContextWindow: 8,
		}},
		SkillPaths: []string{"/skills"},
		MaxRounds:  7,
	}
}

func TestResolveModelConfigExtensionShadowing(t *testing.T) {
	r := loadResolveModelRunner(t)
	cfg := testConfig()

	// Extension-served model: registration drives the config; skills/rounds
	// come from the session config.
	m, err := resolveModelConfig(cfg, r, "echo")
	require.NoError(t, err)
	assert.Equal(t, "echo", m.Name)
	assert.Equal(t, 1000, m.ContextWindow)
	assert.True(t, m.ImageEnabled)
	assert.Equal(t, llm.High, m.Think.Mode)
	assert.True(t, m.Think.Enabled)
	assert.Equal(t, []string{"/skills"}, m.SkillPaths)
	assert.Equal(t, 7, m.MaxRounds)

	// Direct-connect registration: host dials the registered endpoint.
	m, err = resolveModelConfig(cfg, r, "gw")
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:9", m.BaseURL)
	assert.Equal(t, llm.Anthropic, m.API)
	assert.Equal(t, "k", m.APIKey)
	assert.Equal(t, 42, m.ContextWindow)

	// Extension models shadow same-name config entries: the config values for
	// "primary" never leak into "echo".
	m, err = resolveModelConfig(cfg, r, "echo")
	require.NoError(t, err)
	assert.NotEqual(t, "http://primary.example/v1", m.BaseURL)

	// Unknown names keep the primary connection and swap the name only
	// (arbitrary-model workflow).
	m, err = resolveModelConfig(cfg, r, "some-other-model")
	require.NoError(t, err)
	assert.Equal(t, "some-other-model", m.Name)
	assert.Equal(t, "http://primary.example/v1", m.BaseURL)
	assert.Equal(t, "sk-p", m.APIKey)

	// Nil runner behaves like "no extensions".
	m, err = resolveModelConfig(cfg, nil, "primary")
	require.NoError(t, err)
	assert.Equal(t, "primary", m.Name)
}

func TestExtensionModelConfigValidation(t *testing.T) {
	cfg := testConfig()

	t.Run("direct-connect needs base_url", func(t *testing.T) {
		_, err := extensionModelConfig(
			extension.ModelRoute{Reg: pxb.RegisterModel{Name: "x"}},
			cfg, 7,
		)
		require.ErrorContains(t, err, "needs base_url")
	})

	t.Run("unknown preset rejected", func(t *testing.T) {
		_, err := extensionModelConfig(extension.ModelRoute{Reg: pxb.RegisterModel{
			Name: "x", Conn: pxb.ConnExt, Preset: "not-a-model",
		}}, cfg, 7)
		require.ErrorContains(t, err, "unknown preset")
	})

	t.Run("ext-served without base_url is fine", func(t *testing.T) {
		m, err := extensionModelConfig(extension.ModelRoute{Reg: pxb.RegisterModel{
			Name: "x", Conn: pxb.ConnExt, ContextWindow: 5,
		}}, cfg, 7)
		require.NoError(t, err)
		assert.Equal(t, 5, m.ContextWindow)
	})
}
