package controller

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/project"
)

func TestNewController_ReadyEngine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PHI_MODEL", "test-model")
	t.Setenv("PHI_API_KEY", "test-key")
	t.Setenv("PHI_BASE_URL", "http://127.0.0.1:9")

	cwd := t.TempDir()
	proj, err := project.Discover(cwd)
	require.NoError(t, err)
	require.NoError(t, proj.LoadConfig())

	bus := NewBus(nil)
	ctrl, err := NewController(bus, proj)
	require.NoError(t, err)
	require.NotNil(t, ctrl)
	require.NotNil(t, ctrl.engine)
	assert.Equal(t, cwd, ctrl.cwd)
	assert.NotEmpty(t, ctrl.sessionDir)
	assert.Same(t, proj, ctrl.proj)
}

// mcp.json typos must not stop the session: the pool stays nil and startup wins.
func TestNewControllerMalformedMCPConfigFailsOpen(t *testing.T) {
	ctrl := newControllerWithMCPConfig(t, "{ not json")

	assert.Nil(t, ctrl.mcpPool)
}

func TestNewControllerLoadsMCPPool(t *testing.T) {
	ctrl := newControllerWithMCPConfig(t, `{"servers":{"echo":{"transport":"stdio","command":["true"]}}}`)

	require.NotNil(t, ctrl.mcpPool)
	assert.Equal(t, []string{"echo"}, ctrl.mcpPool.ServerNames())
}

// newControllerWithMCPConfig builds a controller whose project mcp.json holds raw.
func newControllerWithMCPConfig(t *testing.T, raw string) *EngineController {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PHI_MODEL", "test-model")
	t.Setenv("PHI_API_KEY", "test-key")
	t.Setenv("PHI_BASE_URL", "http://127.0.0.1:9")
	t.Setenv("PHI_MCP", "") // an inherited PHI_MCP=off would make every pool nil

	cwd := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(cwd, ".phi"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, ".phi", "mcp.json"), []byte(raw), 0o600))

	proj, err := project.Discover(cwd)
	require.NoError(t, err)
	ctrl, err := NewController(NewBus(nil), proj)
	require.NoError(t, err)
	t.Cleanup(ctrl.Close)
	return ctrl
}

func TestRedrawRelay_BindAfterBus(t *testing.T) {
	relay := NewRedrawRelay()
	bus := NewBus(relay.Fire)
	var n int
	relay.Bind(func() { n++ })
	bus.Publish(SubmitMsg{Text: "y"})
	assert.GreaterOrEqual(t, n, 1)

	// Drain so the next Publish can re-arm wake + Fire.
	_ = bus.Drain()
	bus.Publish(SubmitMsg{Text: "z"})
	assert.GreaterOrEqual(t, n, 2)
}

func TestUnsupportedThinkingChangeKeepsSessionConfig(t *testing.T) {
	ctrl := &EngineController{modelCfg: llm.ModelConfig{
		Name: "group/auto-glm-5-3-flash", Think: llm.ThinkConfig{Enabled: true, Mode: llm.Max},
	}}
	require.ErrorContains(t, ctrl.SetThinkLevel(llm.Medium), "use low, high, max")
	assert.Equal(t, llm.Max, ctrl.ThinkLevel())
	assert.Equal(t, []llm.ThinkMode{llm.Low, llm.High, llm.Max}, ctrl.ThinkingModes())
}
