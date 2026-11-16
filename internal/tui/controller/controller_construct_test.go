package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/extension/loader"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/project"
)

// newControllerWithModel seeds a bare controller whose active model lives in
// the configurator, mirroring production wiring without an engine.
func newControllerWithModel(cfg llm.ModelConfig) *EngineController {
	configurator := agent.NewEngineConfigurator()
	configurator.Set(agent.EngineSpec{Model: cfg})
	return &EngineController{configurator: configurator}
}

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

// Supplied collaborators must land in the controller untouched: construction
// assembles only what the options leave out.
func TestNewControllerOptionsInjectCollaborators(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PHI_MODEL", "test-model")
	t.Setenv("PHI_API_KEY", "test-key")
	t.Setenv("PHI_BASE_URL", "http://127.0.0.1:9")
	t.Setenv(loader.EnvExtensions, "off")

	proj, err := project.Discover(t.TempDir())
	require.NoError(t, err)

	runner, _, err := extension.Load(t.TempDir(), "")
	require.NoError(t, err)
	require.NotNil(t, runner)
	jobs, err := agent.NewJobManager(t.TempDir(), llm.ModelConfig{Name: "test-model"},
		agent.WithJobsModelFunc(func(job.Role) llm.ModelConfig { return llm.ModelConfig{Name: "test-model"} }))
	require.NoError(t, err)
	pool := mcp.NewPool(map[string]mcp.ServerConfig{
		"echo": {Command: []string{"true"}},
	})
	require.NoError(t, pool.Close()) // closed pool is fine as an injected placeholder

	configurator := agent.NewEngineConfigurator()
	ctrl, err := NewController(NewBus(nil), proj,
		WithConfigurator(configurator),
		WithExtensions(runner),
		WithJobs(jobs),
		WithMCP(pool),
	)
	require.NoError(t, err)
	t.Cleanup(ctrl.Close)

	assert.Same(t, configurator, ctrl.configurator)
	assert.Same(t, runner, ctrl.Extensions())
	assert.Same(t, jobs, ctrl.jobs)
	assert.Same(t, pool, ctrl.mcpPool)
}

func TestBusConstructorRedraw(t *testing.T) {
	var n int
	bus := NewBus(func() { n++ })
	bus.Publish(SubmitMsg{Text: "y"})
	assert.GreaterOrEqual(t, n, 1)

	// Drain so the next Publish can re-arm wake + Fire.
	_ = bus.Drain()
	bus.Publish(SubmitMsg{Text: "z"})
	assert.GreaterOrEqual(t, n, 2)
}

func TestUnsupportedThinkingChangeKeepsSessionConfig(t *testing.T) {
	ctrl := newControllerWithModel(llm.ModelConfig{
		Name: "group/auto-glm-5-3-flash", Think: llm.ThinkConfig{Enabled: true, Mode: llm.Max},
	})
	require.ErrorContains(t, ctrl.SetThinkLevel(llm.Medium), "use low, high, max")
	assert.Equal(t, llm.Max, ctrl.ThinkLevel())
	assert.Equal(t, []llm.ThinkMode{llm.Low, llm.High, llm.Max}, ctrl.ThinkingModes())
}
