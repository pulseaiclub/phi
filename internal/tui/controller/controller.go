package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/debuglog"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/project/model"
)

const defaultAskTimeoutSec = 120

// EngineController owns agent.Engine lifecycle and stream cancellation.
// It talks to the UI only by publishing Msg values onto the Bus.
//
// The controller is faceted by responsibility; this file keeps the core
// (construction, model orchestration, teardown), and the rest lives beside it:
//
//	assembly.go  shared engine wiring kernel (extensions, MCP, jobs, role models)
//	asks.go      permission gate + overlay ask RPCs
//	stream.go    the off-UI-goroutine agent run loop and cancellation
//	progress.go  job → bus progress pipeline (subscribe + dedup)
//	exthost.go   extension host binding, reload, session lifecycle events
//	sessions.go  resume / new session switching and engine construction
type EngineController struct {
	engine *agent.Engine
	proj   *project.Project

	streamMu     sync.Mutex
	streamCancel context.CancelFunc
	streamGen    int

	bus *Bus

	sessionDir string
	cwd        string
	modelMu    sync.RWMutex // Protect model snapshots read by background jobs.
	modelCfg   llm.ModelConfig

	// asm holds the environment-derived engine collaborators (extension
	// runner, MCP pool, sub-agent jobs, role models); see assembly.go.
	asm       *Assembly
	unsubJobs func()

	gate          permission.Gate
	askTimeoutSec int
	allowAll      atomic.Bool
	agentsEnabled atomic.Bool

	lastJobProgress sync.Map // job slot key → last published signature
}

// NewController returns a live EngineController. proj must be non-nil.
// Failure returns (nil, err), never a half-initialized value.
func NewController(bus *Bus, proj *project.Project, cwd string) (*EngineController, error) {
	if strings.TrimSpace(cwd) == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("tui: getwd: %w", err)
		}
	}

	if err := proj.LoadConfig(); err != nil {
		return nil, err
	}
	config := proj.Config()

	c := &EngineController{
		bus:           bus,
		proj:          proj,
		cwd:           cwd,
		sessionDir:    proj.SessionDir(),
		askTimeoutSec: defaultAskTimeoutSec,
		modelCfg:      config.Model(),
	}
	// TUI defaults to bypass; palette → settings → permissions toggles it.
	c.allowAll.Store(true)

	c.initGate(config.Permissions)
	c.agentsEnabled.Store(config.Agents.Enabled)

	asm, err := NewAssembly(proj, AssemblyOpts{
		RoleModels:  roleModelsFromConfig(config),
		ParentModel: c.modelSnapshot,
	})
	if err != nil {
		return nil, err
	}
	c.asm = asm
	c.bindExtensionHost(asm.Ext())

	eng, _, err := c.openEngine(c.modelCfg, "")
	if err != nil {
		return nil, err
	}
	c.engine = eng
	c.startJobProgress()
	c.emitSessionStart("startup", eng.SessionID(), "")
	return c, nil
}

// roleModelsFromConfig reads the agents.*.model overrides from config.
func roleModelsFromConfig(config *project.Config) map[job.Role]string {
	overrides := make(map[job.Role]string)
	m := config.Agents.Models
	for role, name := range map[job.Role]string{
		job.RoleExplore: m.Explore,
		job.RoleReview:  m.Review,
		job.RoleWorker:  m.Worker,
	} {
		if name != "" {
			overrides[role] = name
		}
	}
	return overrides
}

func (c *EngineController) SetAgentsEnabled(v bool) {
	c.agentsEnabled.Store(v)
	c.engine.SetJobs(c.engineJobs())
}

// AgentsEnabled reports whether sub-agent (agent_*) tools are registered.
func (c *EngineController) AgentsEnabled() bool { return c.agentsEnabled.Load() }

func (c *EngineController) engineJobs() *job.Manager {
	if !c.agentsEnabled.Load() {
		return nil
	}
	return c.asm.Jobs()
}

// RoleModel returns the session-only model name for a sub-agent role.
// Empty means the role inherits the parent model.
func (c *EngineController) RoleModel(role string) string {
	r, err := job.ParseRole(role)
	if err != nil {
		return ""
	}
	return c.asm.RoleModel(r)
}

// SetRoleModel sets the session-only model name for a sub-agent role.
// Empty name clears the override (inherit parent). Name must exist in config.
func (c *EngineController) SetRoleModel(role, name string) error {
	r, err := job.ParseRole(role)
	if err != nil {
		return err
	}
	return c.asm.SetRoleModel(r, name)
}

// SaveDefaultModel changes the global default without rewriting any session.
func (c *EngineController) SaveDefaultModel(name string) error {
	return project.SetDefaultModel(c.proj.Global(), name)
}

func (c *EngineController) SetModel(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("empty model name")
	}
	if err := c.proj.LoadConfig(); err != nil {
		return err
	}
	cfg, err := resolveModelConfig(c.proj.Config(), c.Extensions(), name)
	if err != nil {
		return err
	}
	c.Cancel()
	c.initGate(c.proj.Config().Permissions)
	c.engine.SetPermission(c.gate, c.askPermission)
	c.engine.SetContinueAsk(c.askContinue)
	c.engine.SetJobs(c.engineJobs())
	if _, _, err := c.ReloadExtensions(); err != nil {
		debuglog.Logf("extension: reload on SetModel: %v", err)
	}
	c.modelMu.Lock()
	defer c.modelMu.Unlock()
	// The palette budget belongs to the session, independent of the model.
	cfg.MaxRounds = c.modelCfg.MaxRounds
	if err := c.engine.Session().SetModel(name); err != nil {
		return err
	}
	c.engine.SetModelWithHooks(cfg, model.HooksForConfig(cfg))
	c.modelCfg = cfg
	return nil
}

// SetThinkLevel changes the thinking level for the current session.
func (c *EngineController) SetThinkLevel(mode llm.ThinkMode) error {
	c.modelMu.Lock()
	defer c.modelMu.Unlock()
	cfg := c.modelCfg
	cfg.Think = llm.ThinkConfig{Mode: mode, Enabled: mode != llm.Off}
	if err := model.ValidateThinking(cfg); err != nil {
		return err
	}
	c.engine.SetModel(cfg)
	c.modelCfg = cfg
	return nil
}

// ThinkingModes returns the choices supported by the active model and endpoint.
func (c *EngineController) ThinkingModes() []llm.ThinkMode {
	return model.ThinkingModes(c.modelSnapshot())
}

// ThinkLevel returns the current thinking mode.
func (c *EngineController) ThinkLevel() llm.ThinkMode {
	return c.modelSnapshot().Think.Mode
}

// ModelName returns the active model name.
func (c *EngineController) ModelName() string {
	return c.modelSnapshot().Name
}

// ContextWindow returns the active model's context window in tokens, so the
// UI can re-render context pressure after a model switch.
func (c *EngineController) ContextWindow() int {
	return c.modelSnapshot().ContextWindow
}

// MaxRounds returns the session's tool round budget; zero means the engine default.
func (c *EngineController) MaxRounds() int {
	return c.modelSnapshot().MaxRounds
}

func (c *EngineController) modelSnapshot() llm.ModelConfig {
	c.modelMu.RLock()
	defer c.modelMu.RUnlock()
	return c.modelCfg
}

// SetMaxRounds updates the session-only max rounds budget.
func (c *EngineController) SetMaxRounds(n int) error {
	if n <= 0 {
		return fmt.Errorf("max rounds must be positive, got %d", n)
	}
	c.modelMu.Lock()
	defer c.modelMu.Unlock()
	c.modelCfg.MaxRounds = n
	c.engine.SetMaxRounds(n)
	return nil
}

func (c *EngineController) ImageEnabled() bool {
	return c.modelSnapshot().ImageEnabled
}

func (c *EngineController) SessionDir() string {
	return c.sessionDir
}

func (c *EngineController) LiveJobCount() int {
	return c.asm.Jobs().LiveCount()
}

func (c *EngineController) Close() {
	c.sessionShutdown("quit", c.SessionID())
	c.Cancel()
	c.unsubJobs()
	_ = c.asm.Jobs().Close(context.Background())
	if pool := c.asm.MCP(); pool != nil {
		_ = pool.Close()
	}
	if prev := c.asm.SwapExt(nil); prev != nil {
		prev.Close()
	}
}

func (c *EngineController) publish(m Msg) {
	c.bus.Publish(m)
}
