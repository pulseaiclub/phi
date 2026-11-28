package controller

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/extension/loader"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/components/toast"
	coretypes "github.com/pulseaiclub/phi/internal/core"
	"github.com/pulseaiclub/phi/internal/debuglog"
	"github.com/pulseaiclub/phi/internal/extension/core"
	"github.com/pulseaiclub/phi/internal/extension/ui"
	"github.com/pulseaiclub/phi/internal/job"

	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/project/model"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

// ExtRunner is the controller's handle on the extension host: the management
// surface the TUI drives, the models and slash commands it consults, and the
// engine-facing dispatch surface handed to agent.EngineConfig. The
// production implementation is *core.Runner.
type ExtRunner interface {
	extension.Extensions

	// Lifecycle management.
	Loaded() []loader.Discovered
	ListExtensions() map[string]core.FiberState
	UnloadExtension(name string) error
	ReloadExtension(name string) error
	Close()

	// Engine wiring.
	SetBaseTools(base []tooldef.Tool)
	SetMeta(sessionID, cwd string)

	// Models and slash commands.
	ExtensionModels() []core.ModelRoute
	CommandEntries() []ext.CommandEntry
	RunCommand(name, args string) (core.CommandOutcome, error)

	// Session lifecycle events.
	EmitSessionStart(ev ext.SessionStartEvent) ext.SessionEffects
	EmitSessionShutdown(ev ext.SessionShutdownEvent) ext.SessionEffects
	EmitSessionBeforeSwitch(ev ext.SessionBeforeSwitchEvent) ext.SessionEffects
}

const defaultAskTimeoutSec = 120

// EngineController owns agent.Engine lifecycle and stream cancellation.
// It talks to the UI only by publishing Msg values onto the Bus.
type EngineController struct {
	engine *agent.Engine
	sess   *agent.Session // bound to engine; the two swap together
	proj   *project.Project

	streamMu       sync.Mutex
	streamCancel   context.CancelFunc
	compactCancel  context.CancelFunc
	streamGen      int
	lastPrompt     LastPrompt
	promptAppended bool
	retryReady     bool

	bus *Bus

	sessionDir string
	cwd        string
	modelMu    sync.RWMutex // Protect engine/session/model snapshots read by background jobs and local bash.
	modelCfg   coretypes.ModelConfig
	// roleModels maps sub-agent role → model name. Empty → inherit modelCfg.
	roleModels map[job.Role]string
	jobs       *job.Manager
	unsubJobs  func()

	askTimeoutSec int
	agentsEnabled atomic.Bool
	extRunner     atomic.Pointer[core.Runner]
	mcpPool       *mcp.Pool

	lastJobProgress sync.Map // progressSlot → progressSig
}

// NewController returns a live EngineController. proj must be non-nil.
// Failure returns (nil, err), never a half-initialized value.
func NewController(bus *Bus, proj *project.Project) (controller *EngineController, err error) {
	if err := proj.LoadConfig(); err != nil {
		return nil, err
	}
	config := proj.Config()
	cwd := proj.Root()
	sessionDir := proj.SessionDir()
	modelCfg := config.Model()
	roleModels := config.Agents.Models.RoleOverrides()

	c := &EngineController{
		bus:           bus,
		proj:          proj,
		cwd:           cwd,
		sessionDir:    sessionDir,
		askTimeoutSec: defaultAskTimeoutSec,
		modelCfg:      modelCfg,
		roleModels:    roleModels,
	}
	c.agentsEnabled.Store(config.Agents.Enabled)

	sessOpts := []agent.SessionOption{
		agent.WithCwd(cwd),
		agent.WithSessionDir(sessionDir),
		agent.WithPersist(true),
		agent.WithModel(modelCfg.Name),
	}
	sess, err := agent.NewSession(sessOpts...)
	if err != nil {
		return nil, err
	}

	jobs, err := agent.NewJobManager(
		proj.JobsDir(),
		roleModelConfigs(modelCfg, roleModels, nil, config),
		func() extension.Extensions {
			return c.Extensions()
		},
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = jobs.Close(context.Background())
		}
	}()
	c.jobs = jobs

	var mcpPool *mcp.Pool
	if pool, loadErr := mcp.LoadPool(proj.MCPConfigFile()); loadErr != nil {
		debuglog.Logf("mcp: load: %v", loadErr)
	} else {
		mcpPool = pool
		c.mcpPool = pool
		defer func() {
			if err != nil {
				_ = mcpPool.Close()
			}
		}()
	}

	r := loadStartupExtensions(proj, sess, jobs, config.Agents.Enabled, mcpPool, bus, defaultAskTimeoutSec)
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	eng, err := buildEngine(modelCfg, r, sess, jobs, config.Agents.Enabled, mcpPool)
	if err != nil {
		return nil, err
	}
	c.extRunner.Store(r)
	jobs.SetRoleModels(roleModelConfigs(modelCfg, roleModels, r, config))
	c.engine = eng
	c.sess = sess
	ch, cancel := c.jobs.Subscribe()
	c.unsubJobs = cancel
	go func() {
		for p := range ch {
			if c.shouldPublishJobProgress(p) {
				c.publish(JobProgressMsg{Progress: p})
			}
		}
	}()
	c.emitSessionStart("startup", "")
	return c, nil
}

// progressSlot identifies the UI row a job progress update belongs to: the
// tool_use id when known, else a name+detail slot. The parts stay separate
// fields rather than a joined string so a field containing the separator
// cannot make the tool-id branch collide with the name+detail branch.
type progressSlot struct {
	job    string
	tool   string
	name   string
	detail string
}

// progressSig holds the fields a slot's UI row renders; an unchanged sig means
// the publish would not change the UI.
type progressSig struct {
	status string
	name   string
	detail string
}

func (c *EngineController) shouldPublishJobProgress(p job.Progress) bool {
	slot := progressSlot{job: p.JobID, tool: p.ToolUseID}
	if p.ToolUseID == "" {
		slot.name = p.Name
		slot.detail = p.Detail
	}
	sig := progressSig{status: p.Status, name: p.Name, detail: p.Detail}
	if prev, ok := c.lastJobProgress.Load(slot); ok && prev.(progressSig) == sig {
		return false
	}
	c.lastJobProgress.Store(slot, sig)
	return true
}

func (c *EngineController) SetAgentsEnabled(v bool) error {
	sess, modelCfg, r := c.sess, c.modelSnapshot(), c.Extensions()
	cfg := c.engineConfig(modelCfg, r)
	cfg.Jobs = nil
	if v {
		cfg.Jobs = c.jobs
	}
	eng, err := cfg.NewEngine(modelCfg, sess)
	if err != nil {
		return err
	}
	r.SetBaseTools(cfg.CoreTools(sess))
	c.agentsEnabled.Store(v)
	c.installEngine(eng, sess, modelCfg)
	return nil
}

// AgentsEnabled reports whether sub-agent (agent_*) tools are registered.
func (c *EngineController) AgentsEnabled() bool { return c.agentsEnabled.Load() }

func lookupExtensionModel(extRunner extension.Extensions, name string) (core.ModelRoute, bool) {
	if extRunner == nil {
		return core.ModelRoute{}, false
	}
	return extRunner.LookupModel(name)
}

func roleModelConfigs(
	parent coretypes.ModelConfig,
	roleModels map[job.Role]string,
	extRunner extension.Extensions,
	config *project.Config,
) map[job.Role]coretypes.ModelConfig {
	models := map[job.Role]coretypes.ModelConfig{
		job.RoleExplore: parent,
		job.RoleReview:  parent,
		job.RoleWorker:  parent,
	}
	for role, name := range roleModels {
		if route, ok := lookupExtensionModel(extRunner, name); ok {
			m, err := extensionModelConfig(route, config)
			if err == nil {
				models[role] = m
				continue
			}
			debuglog.Logf("agents: extension role model %q: %v; using parent", name, err)
		} else if m, ok := config.FindModel(name); ok {
			models[role] = m
			continue
		} else {
			debuglog.Logf("agents: unknown role model %q for %s; using parent", name, role)
		}
	}
	return models
}

// RoleModel returns the session-only model name for a sub-agent role.
// Empty means the role inherits the parent model.
func (c *EngineController) RoleModel(role string) string {
	r, err := job.ParseRole(role)
	if err != nil {
		return ""
	}
	c.modelMu.RLock()
	defer c.modelMu.RUnlock()
	return c.roleModels[r]
}

// SetRoleModel sets the session-only model name for a sub-agent role.
// Empty name clears the override (inherit parent). Name must exist in config.
func (c *EngineController) SetRoleModel(role, name string) error {
	r, err := job.ParseRole(role)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name != "" {
		if _, ok := c.proj.Config().FindModel(name); !ok {
			if _, ok := c.Extensions().LookupModel(name); !ok {
				return fmt.Errorf("unknown model %q", name)
			}
		}
	}
	c.modelMu.Lock()
	defer c.modelMu.Unlock()
	if name == "" {
		delete(c.roleModels, r)
	} else {
		c.roleModels[r] = name
	}
	c.jobs.SetRoleModels(roleModelConfigs(c.modelCfg, c.roleModels, c.Extensions(), c.proj.Config()))
	return nil
}

func (c *EngineController) refreshJobRoleModels() {
	c.modelMu.RLock()
	defer c.modelMu.RUnlock()
	c.jobs.SetRoleModels(roleModelConfigs(c.modelCfg, c.roleModels, c.Extensions(), c.proj.Config()))
}

func (c *EngineController) engineJobs() *job.Manager {
	if !c.agentsEnabled.Load() {
		return nil
	}
	return c.jobs
}

// Extensions returns the runner handle. The value is never a nil interface —
// the disabled state is a nil *core.Runner behind ExtRunner, whose
// methods are nil-receiver no-ops. Use HasExtensions for the presence check.
func (c *EngineController) Extensions() ExtRunner {
	return c.extRunner.Load()
}

// HasExtensions reports whether a runner is actually loaded.
func (c *EngineController) HasExtensions() bool {
	return c.extRunner.Load() != nil
}

func (c *EngineController) ReloadExtensions() (loaded int, warns []loader.Warning, err error) {
	sess := c.sess
	r, warns, err := c.loadExtensions(sess)
	if err != nil {
		return 0, warns, err
	}
	modelCfg := c.modelSnapshot()
	eng, err := c.newEngine(modelCfg, r, sess)
	if err != nil {
		r.Close()
		return 0, warns, err
	}
	c.swapExtensionRunner(r)
	c.installEngine(eng, sess, modelCfg)
	if r == nil {
		return 0, warns, nil
	}
	return len(r.Loaded()), warns, nil
}

func (c *EngineController) swapExtensionRunner(r *core.Runner) {
	if prev := c.extRunner.Swap(r); prev != nil {
		prev.Close()
	}
	// Previous runner's UI state died with its subprocesses.
	c.publish(ExtSessionEffectsMsg{Status: "", StatusSet: true})
}

func (c *EngineController) ListExtensions() ([]loader.Discovered, []loader.Warning, error) {
	return loader.Discover(c.proj.Global().ExtensionsDir(), c.proj.ExtensionsDir())
}

// ReloadExtension hot-reloads a single extension by name.
func (c *EngineController) ReloadExtension(name string) error {
	r := c.extRunner.Load()
	if r == nil {
		return errors.New("no extension runner loaded")
	}
	err := r.ReloadExtension(name)
	c.refreshJobRoleModels()
	return err
}

// UnloadExtension unloads a single extension by name.
func (c *EngineController) UnloadExtension(name string) error {
	r := c.extRunner.Load()
	if r == nil {
		return errors.New("no extension runner loaded")
	}
	err := r.UnloadExtension(name)
	c.refreshJobRoleModels()
	return err
}

// ListExtensionStates returns all extension states.
func (c *EngineController) ListExtensionStates() map[string]core.FiberState {
	r := c.extRunner.Load()
	if r == nil {
		return nil
	}
	return r.ListExtensions()
}

func loadStartupExtensions(
	proj *project.Project,
	sess *agent.Session,
	jobs *job.Manager,
	agentsEnabled bool,
	mcpPool *mcp.Pool,
	bus *Bus,
	askTimeoutSec int,
) *core.Runner {
	base := agent.EngineConfig{MCP: mcpPool}
	if agentsEnabled {
		base.Jobs = jobs
	}
	host := ext.HostOpts{
		UI: ui.BusUI{
			NotifyFn: func(message, kind string) { publishExtNotify(bus, message, kind) },
			SetStatusFn: func(_, text string) {
				bus.Publish(ExtSessionEffectsMsg{Status: text, StatusSet: true})
			},
			ConfirmFn: func(req ext.ConfirmRequest) ext.ConfirmReply {
				return askExtConfirm(bus, req, askTimeoutDuration(askTimeoutSec))
			},
		},
		Cwd: sess.Cwd(), SessionID: sess.ID(), HasUI: true,
		RefreshTools: func() { bus.Publish(ExtTopologyChangedMsg{}) },
	}
	r, warns, err := extension.LoadWithHost(
		proj.Global().ExtensionsDir(),
		proj.ExtensionsDir(),
		core.HostConfig{
			Host: host, BaseTools: base.CoreTools(sess),
			OnChange: func() { bus.Publish(ExtTopologyChangedMsg{}) },
			OnPrompt: func(r *core.Runner, sessionID, text string) {
				bus.Publish(ExtPromptMsg{runner: r, sessionID: sessionID, text: text})
			},
		},
	)
	for _, w := range warns {
		debuglog.Logf("extension: %s", w.String())
	}
	if err != nil {
		debuglog.Logf("extension: load failed: %v", err)
	}
	return r
}

func buildEngine(
	cfg coretypes.ModelConfig,
	extRunner extension.Extensions,
	sess *agent.Session,
	jobs *job.Manager,
	agentsEnabled bool,
	mcpPool *mcp.Pool,
) (*agent.Engine, error) {
	if !agentsEnabled {
		jobs = nil
	}
	return (agent.EngineConfig{
		Jobs: jobs, Extensions: extRunner, MCP: mcpPool, Hooks: model.HooksForConfig(cfg),
	}).NewEngine(cfg, sess)
}

// Startup and resume are fail-open; an explicit reload reports discovery errors.
func (c *EngineController) loadSessionExtensions(sess *agent.Session) *core.Runner {
	r, _, err := c.loadExtensions(sess)
	if err != nil {
		debuglog.Logf("extension: load failed: %v", err)
	}
	return r
}

func (c *EngineController) loadExtensions(sess *agent.Session) (*core.Runner, []loader.Warning, error) {
	host := ext.HostOpts{
		UI: ui.BusUI{
			NotifyFn: c.handleExtNotify, SetStatusFn: c.handleExtStatus, ConfirmFn: c.askExtConfirm,
		},
		Cwd: sess.Cwd(), SessionID: sess.ID(), HasUI: true,
		RefreshTools: c.requestTopologyRefresh,
	}
	base := agent.EngineConfig{Jobs: c.engineJobs(), MCP: c.mcpPool}
	r, warns, err := extension.LoadWithHost(
		c.proj.Global().ExtensionsDir(),
		c.proj.ExtensionsDir(),
		core.HostConfig{
			Host: host, BaseTools: base.CoreTools(sess), OnChange: c.requestTopologyRefresh,
			OnPrompt: func(r *core.Runner, sessionID, text string) {
				c.publish(ExtPromptMsg{runner: r, sessionID: sessionID, text: text})
			},
		},
	)
	for _, w := range warns {
		debuglog.Logf("extension: %s", w.String())
	}
	return r, warns, err
}

// StartExtensionPrompt runs on the UI thread, after any pending runner or session switch.
func (c *EngineController) StartExtensionPrompt(m ExtPromptMsg) {
	if m.runner != nil && m.runner == c.Extensions() && c.sess != nil && m.sessionID == c.SessionID() {
		c.StartPrompt(m.text, nil, nil)
	}
}

func (c *EngineController) handleExtNotify(message, kind string) {
	publishExtNotify(c.bus, message, kind)
}

func (c *EngineController) handleExtStatus(_, text string) {
	c.publish(ExtSessionEffectsMsg{Status: text, StatusSet: true})
}

func (c *EngineController) requestTopologyRefresh() {
	c.publish(ExtTopologyChangedMsg{})
}

// SyncExtensionTools replaces the engine for future turns on the UI goroutine.
func (c *EngineController) SyncExtensionTools() error {
	if c.sess == nil {
		return nil
	}
	modelCfg := c.modelSnapshot()
	eng, err := c.newEngine(modelCfg, c.Extensions(), c.sess)
	if err != nil {
		return err
	}
	c.installEngine(eng, c.sess, modelCfg)
	return nil
}

func (c *EngineController) askTimeout() time.Duration {
	return askTimeoutDuration(c.askTimeoutSec)
}

func askTimeoutDuration(seconds int) time.Duration {
	if seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return time.Duration(defaultAskTimeoutSec) * time.Second
}

func waitReply[T any](ctx context.Context, ch <-chan T, timeout time.Duration, onAbort func()) (T, error) {
	var zero T
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r, nil
	case <-ctx.Done():
		onAbort()
		return zero, ctx.Err()
	case <-timer.C:
		onAbort()
		return zero, nil
	}
}

func (c *EngineController) askExtConfirm(req ext.ConfirmRequest) ext.ConfirmReply {
	return askExtConfirm(c.bus, req, c.askTimeout())
}

func askExtConfirm(bus *Bus, req ext.ConfirmRequest, timeout time.Duration) ext.ConfirmReply {
	reply := make(chan ExtConfirmReply, 1)
	bus.Publish(OverlayMsg{
		Kind:         OverlayExtConfirm,
		Title:        req.Title,
		Message:      req.Message,
		Yes:          req.Yes,
		No:           req.No,
		Danger:       req.Danger,
		ConfirmReply: reply,
	})
	r, _ := waitReply(context.Background(), reply, timeout, func() {
		bus.Publish(OverlayMsg{Kind: OverlayExtConfirmDismiss})
	})
	return ext.ConfirmReply{OK: r.OK}
}

func publishExtNotify(bus *Bus, message, kind string) {
	toastKind := toast.ToastSuccess
	switch strings.ToLower(kind) {
	case "warning":
		toastKind = toast.ToastWarning
	case "error":
		toastKind = toast.ToastError
	}
	bus.Publish(ToastMsg{Message: message, Kind: toastKind, Duration: 3 * time.Second})
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
	sess := c.sess
	r, _, loadErr := c.loadExtensions(sess)
	if loadErr != nil {
		debuglog.Logf("extension: reload on SetModel: %v", loadErr)
		r = c.extRunner.Load()
	}
	cfg, err := resolveModelConfig(c.proj.Config(), r, name)
	var eng *agent.Engine
	if err == nil {
		eng, err = c.newEngine(cfg, r, sess)
	}
	if err == nil {
		c.Cancel()
		err = sess.SetModel(name)
	}
	if err != nil {
		if loadErr == nil {
			r.Close()
		}
		return err
	}
	if loadErr == nil {
		c.swapExtensionRunner(r)
	}
	c.installEngine(eng, sess, cfg)
	return nil
}

// SetThinkLevel changes the thinking level for the current session.
func (c *EngineController) SetThinkLevel(mode coretypes.ThinkMode) error {
	cfg := c.modelSnapshot()
	cfg.Think = coretypes.ThinkConfig{Mode: mode, Enabled: mode != coretypes.Off}
	if err := model.ValidateThinking(cfg); err != nil {
		return err
	}
	eng, err := c.newEngine(cfg, c.Extensions(), c.sess)
	if err != nil {
		return err
	}
	c.installEngine(eng, c.sess, cfg)
	return nil
}

// ThinkingModes returns the choices supported by the active model and endpoint.
func (c *EngineController) ThinkingModes() []coretypes.ThinkMode {
	return model.ThinkingModes(c.modelSnapshot())
}

// ThinkLevel returns the current thinking mode.
func (c *EngineController) ThinkLevel() coretypes.ThinkMode {
	think := c.modelSnapshot().Think
	if !think.Enabled && think.Mode != "" {
		return coretypes.Off
	}
	return think.Mode
}

// ModelName returns the active model name.
func (c *EngineController) ModelName() string {
	return c.modelSnapshot().Name
}

// ContextWindow returns the active model's effective compaction window in
// tokens, so the UI context-pressure indicator matches the compaction threshold.
func (c *EngineController) ContextWindow() int {
	return c.modelSnapshot().CompactionWindow()
}

func (c *EngineController) modelSnapshot() coretypes.ModelConfig {
	c.modelMu.RLock()
	defer c.modelMu.RUnlock()
	return c.modelCfg
}

func (c *EngineController) ImageEnabled() bool {
	return c.modelSnapshot().ImageEnabled
}

func (c *EngineController) SessionID() string {
	return c.sess.ID()
}

// AppendUserBash records a user "!!cmd" run in the current session so the next
// turn includes its output. sessionID guards a session switch that happened
// while the command was running: the output belongs to the old session, not
// whichever one is open now.
func (c *EngineController) AppendUserBash(sessionID, command, output string, exitCode int, cancelled bool) error {
	c.modelMu.RLock()
	sess := c.sess
	c.modelMu.RUnlock()
	if sessionID != "" && sess.ID() != sessionID {
		return nil
	}
	return sess.AppendBash(command, output, exitCode, cancelled)
}

func (c *EngineController) SessionDir() string {
	return c.sessionDir
}

func (c *EngineController) SessionFile() string {
	return c.sess.File()
}

func (c *EngineController) Resume(id string) (cwdWarning string, err error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("empty session id")
	}
	prevID, err := c.preflightSessionSwitch("resume", id)
	if err != nil {
		return "", err
	}
	cfg, err := c.resolveModel()
	if err != nil {
		return "", err
	}

	sessOpts := []agent.SessionOption{
		agent.WithCwd(c.cwd),
		agent.WithSessionDir(c.sessionDir),
		agent.WithPersist(true),
		agent.WithModel(cfg.Name),
		agent.WithResumeID(id),
	}
	sess, err := agent.NewSession(sessOpts...)
	if err != nil {
		return "", err
	}
	extRunner := c.loadSessionExtensions(sess)
	if name := sess.ModelName(); name != "" {
		cfg, err = resolveModelConfig(c.proj.Config(), extRunner, name)
		if err != nil {
			extRunner.Close()
			return "", fmt.Errorf("restore session model %q: %w", name, err)
		}
	}
	eng, err := c.newEngine(cfg, extRunner, sess)
	if err != nil {
		extRunner.Close()
		return "", err
	}
	c.commitSessionSwitch("resume", id)
	c.swapExtensionRunner(extRunner)
	if sessCwd := sess.Cwd(); sessCwd != "" && c.cwd != "" && sessCwd != c.cwd {
		cwdWarning = fmt.Sprintf("cwd %s ≠ %s", filepath.Base(sessCwd), filepath.Base(c.cwd))
	}
	c.activateSession(eng, sess, cfg, "resume", prevID)
	return cwdWarning, nil
}

// NewSession starts a brand-new persisted session. Caller must ensure no agent
// stream / local bash is in flight.
func (c *EngineController) NewSession() error {
	prevID, err := c.preflightSessionSwitch("new", "")
	if err != nil {
		return err
	}
	cfg, err := c.resolveModel()
	if err != nil {
		return err
	}
	sessOpts := []agent.SessionOption{
		agent.WithCwd(c.cwd),
		agent.WithSessionDir(c.sessionDir),
		agent.WithPersist(true),
		agent.WithModel(cfg.Name),
	}
	sess, err := agent.NewSession(sessOpts...)
	if err != nil {
		return err
	}
	engine, err := c.newEngine(cfg, c.Extensions(), sess)
	if err != nil {
		return err
	}
	c.commitSessionSwitch("new", "")
	c.switchRunnerSession(sess)
	c.activateSession(engine, sess, cfg, "new", prevID)
	return nil
}

// ForkSession copies the active session's current branch into a new persisted
// session and switches the controller to it. The before-switch hook runs first
// so a denied fork writes nothing, and the fork file exists and is bound to an
// engine before the live session is touched: any failure leaves the source
// session active and its history intact.
func (c *EngineController) ForkSession() (string, error) {
	prevID, err := c.preflightSessionSwitch("fork", "")
	if err != nil {
		return "", err
	}
	forked, err := c.sess.Fork()
	if err != nil {
		return "", err
	}
	cfg, err := c.resolveModel()
	if err != nil {
		c.discardFork(forked.ID())
		return "", err
	}
	eng, err := c.newEngine(cfg, c.Extensions(), forked)
	if err != nil {
		c.discardFork(forked.ID())
		return "", err
	}
	c.commitSessionSwitch("fork", forked.ID())
	c.switchRunnerSession(forked)
	c.activateSession(eng, forked, cfg, "fork", prevID)
	return forked.ID(), nil
}

// activateSession publishes the new session only after installing its engine, session, and model together.
func (c *EngineController) activateSession(
	eng *agent.Engine,
	sess *agent.Session,
	cfg coretypes.ModelConfig,
	reason, prevID string,
) {
	c.installEngine(eng, sess, cfg)
	c.emitSessionStart(reason, prevID)
}

// installEngine swaps the live engine and the session it is bound to as one unit.
func (c *EngineController) installEngine(eng *agent.Engine, sess *agent.Session, cfg coretypes.ModelConfig) {
	c.modelMu.Lock()
	defer c.modelMu.Unlock()
	c.engine = eng
	c.sess = sess
	c.modelCfg = cfg
	c.jobs.SetRoleModels(roleModelConfigs(cfg, c.roleModels, c.Extensions(), c.proj.Config()))
}

// discardFork drops a fork the controller never switched to, so a failed switch
// does not leave an unreachable session in the session list.
func (c *EngineController) discardFork(id string) {
	if _, err := session.DeleteSession(c.sessionDir, id); err != nil {
		debuglog.Logf("session: drop unused fork %s: %v", id, err)
	}
}

// preflightSessionSwitch asks extensions whether a switch may happen. Nothing
// is mutated yet: a denial leaves the current session untouched.
func (c *EngineController) preflightSessionSwitch(reason, targetID string) (prevID string, err error) {
	if c.sessionDir == "" {
		return "", errors.New("session directory not configured")
	}
	prevID = c.SessionID()
	out := c.sessionBeforeSwitch(reason, targetID)
	c.publishSessionEffects(out)
	if out.Denied {
		msg := out.Reason
		if msg == "" {
			msg = "session switch denied by extension"
		}
		return "", errors.New(msg)
	}
	return prevID, nil
}

// commitSessionSwitch retires the outgoing session: the in-flight stream is
// cancelled and extensions see session_shutdown before the new engine is
// installed.
func (c *EngineController) commitSessionSwitch(reason, targetID string) {
	c.Cancel()
	c.sessionShutdown(reason, targetID)
}

func (c *EngineController) resolveModel() (coretypes.ModelConfig, error) {
	if cfg := c.modelSnapshot(); cfg.Name != "" {
		return cfg, nil
	}
	if err := c.proj.LoadConfig(); err != nil {
		return coretypes.ModelConfig{}, err
	}
	return c.proj.Config().Model(), nil
}

func (c *EngineController) switchRunnerSession(sess *agent.Session) {
	r := c.Extensions()
	base := agent.EngineConfig{Jobs: c.engineJobs(), MCP: c.mcpPool}
	r.SetBaseTools(base.CoreTools(sess))
	r.SetMeta(sess.ID(), sess.Cwd())
}

// newEngine binds a session to a fresh engine on the controller-wide gate,
// jobs, extensions, MCP pool and hooks. Every session switch funnels through
// here, so a forked session runs on exactly the wiring of a resumed one.
func (c *EngineController) newEngine(
	cfg coretypes.ModelConfig,
	extRunner extension.Extensions,
	sess *agent.Session,
) (*agent.Engine, error) {
	return c.engineConfig(cfg, extRunner).NewEngine(cfg, sess)
}

func (c *EngineController) engineConfig(cfg coretypes.ModelConfig, r extension.Extensions) agent.EngineConfig {
	return agent.EngineConfig{Jobs: c.engineJobs(), Extensions: r, MCP: c.mcpPool, Hooks: model.HooksForConfig(cfg)}
}

func (c *EngineController) ReplaySnapshot() session.Snapshot {
	return session.ReplaySnapshot(c.sess.PathEntries(), c.engine.ToolDetail)
}

func (c *EngineController) StartPrompt(text string, pendingSkills []string, images []coretypes.Image) {
	c.streamMu.Lock()
	defer c.streamMu.Unlock()
	c.lastPrompt = LastPrompt{
		Text:   text,
		Skills: append([]string(nil), pendingSkills...),
		Images: append([]coretypes.Image(nil), images...),
	}
	c.promptAppended = false
	c.startPromptLocked()
}

func (c *EngineController) startPromptLocked() {
	if c.streamCancel != nil {
		c.streamCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.streamCancel = cancel
	c.streamGen++
	c.retryReady = false
	c.publish(LoopStartedMsg{Gen: c.streamGen})
	go c.runLoop(ctx, c.streamGen, c.engine, c.lastPrompt, c.promptAppended)
}

// Compact runs a manual context compaction for the active session.
//
// The outcome is reported as a toast, not through the loop-error dialog: the
// dialog offers Retry, which replays a prompt, and the user asked for a
// compaction. Esc cancels the run through Cancel, like any other in-flight
// work. Refusing a second concurrent run matters because both would compact
// the same leaf and the loser aborts on the anchor check.
func (c *EngineController) Compact() error {
	ctx, ok := c.beginCompaction()
	if !ok {
		return errors.New("a compaction is already running")
	}
	engine := c.engine
	go c.runCompact(ctx, engine)
	return nil
}

// beginCompaction claims the manual-compaction slot, so prompts and a second
// command cannot act on the session while a summary is in flight.
func (c *EngineController) beginCompaction() (context.Context, bool) {
	c.streamMu.Lock()
	defer c.streamMu.Unlock()
	if c.compactCancel != nil {
		return nil, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.compactCancel = cancel
	return ctx, true
}

// endCompaction releases the slot and cancels the context, so a finished run
// leaves no stale cancellation behind.
func (c *EngineController) endCompaction() {
	c.streamMu.Lock()
	cancel := c.compactCancel
	c.compactCancel = nil
	c.streamMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *EngineController) runCompact(ctx context.Context, engine *agent.Engine) {
	defer c.endCompaction()

	var before int
	did, err := engine.Compact(ctx, func(ev session.Event) bool {
		if done, ok := ev.(session.CompactionComplete); ok && !done.Failed {
			before = done.TokensBefore
		}
		// Gen 0: these events belong to no prompt loop. The footer activity and
		// the compaction marker come from CompactionStarted/Complete themselves.
		c.publish(SessionEventMsg{Event: ev})
		return true
	})

	c.publish(CompactionFinishedMsg{
		Compacted:    did,
		Err:          err,
		Cancelled:    ctx.Err() != nil,
		TokensBefore: before,
	})
}

// RetryLastPrompt resumes a failed turn without appending its user message again.
func (c *EngineController) RetryLastPrompt() bool {
	c.streamMu.Lock()
	defer c.streamMu.Unlock()
	if !c.retryReady {
		return false
	}
	c.startPromptLocked()
	return true
}

func (c *EngineController) Cancel() {
	c.streamMu.Lock()
	defer c.streamMu.Unlock()
	if c.streamCancel != nil {
		c.streamCancel()
	}
	// A manual summary is cancellable too: Esc must not leave it running
	// against a session the user is about to act on.
	if c.compactCancel != nil {
		c.compactCancel()
	}
	// Invalidate queued completion messages as well as the running request.
	c.streamGen++
	c.retryReady = false
}

// CurrentLoop reports whether a queued loop result still belongs to this request.
func (c *EngineController) CurrentLoop(gen int) bool {
	c.streamMu.Lock()
	defer c.streamMu.Unlock()
	return c.streamGen == gen
}

func (c *EngineController) Close() {
	c.sessionShutdown("quit", "")
	c.Cancel()
	c.unsubJobs()
	_ = c.jobs.Close(context.Background())
	if c.mcpPool != nil {
		_ = c.mcpPool.Close()
		c.mcpPool = nil
	}
	if prev := c.extRunner.Swap(nil); prev != nil {
		prev.Close()
	}
}

func (c *EngineController) sessionBeforeSwitch(reason, targetID string) ext.SessionEffects {
	if !c.HasExtensions() {
		return ext.SessionEffects{}
	}
	return c.Extensions().EmitSessionBeforeSwitch(ext.SessionBeforeSwitchEvent{
		Reason:          reason,
		TargetSessionID: targetID,
	})
}

func (c *EngineController) sessionShutdown(reason, targetID string) {
	if !c.HasExtensions() {
		return
	}
	c.publishSessionEffects(c.Extensions().EmitSessionShutdown(ext.SessionShutdownEvent{
		Reason:          reason,
		TargetSessionID: targetID,
	}))
}

func (c *EngineController) emitSessionStart(reason, previousID string) {
	if !c.HasExtensions() {
		return
	}
	c.publishSessionEffects(c.Extensions().EmitSessionStart(ext.SessionStartEvent{
		Reason:            reason,
		PreviousSessionID: previousID,
	}))
}

func (c *EngineController) publishSessionEffects(out ext.SessionEffects) {
	if out.Toast == "" && !out.StatusSet {
		return
	}
	c.publish(ExtSessionEffectsMsg{
		Toast:     out.Toast,
		Status:    out.Status,
		StatusSet: out.StatusSet,
	})
}

func (c *EngineController) waitOrDone(ctx context.Context, gen int, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	}
	return c.CurrentLoop(gen)
}

func (c *EngineController) publish(m Msg) {
	c.bus.Publish(m)
}

func (c *EngineController) finishLoop(ctx context.Context, gen int, err error) {
	c.streamMu.Lock()
	if c.streamGen != gen {
		c.streamMu.Unlock()
		return
	}
	// Only cancellation of this request is silent. Provider timeouts remain errors.
	cancelled := ctx.Err() != nil || errors.Is(err, context.Canceled)
	errText := ""
	if err != nil && !cancelled {
		errText = err.Error()
	}
	c.retryReady = errText != ""
	c.streamMu.Unlock()
	c.publish(LoopFinishedMsg{Gen: gen, ErrorText: errText, Cancelled: cancelled})
}

func (c *EngineController) runLoop(ctx context.Context, gen int, engine *agent.Engine, prompt LastPrompt, resume bool) {
	if !c.waitOrDone(ctx, gen, 120*time.Millisecond) {
		return
	}
	var loopErr error
	// Publish completion only after the iterator's cleanup (including AgentEnd)
	// has finished, so Retry cannot start while the failed turn is unwinding.
	defer func() { c.finishLoop(ctx, gen, loopErr) }()
	for ev, err := range engine.Loop(ctx, prompt.Text, agent.LoopOpts{
		PendingSkills: prompt.Skills,
		Images:        prompt.Images,
		Resume:        resume,
		OnPromptAppended: func() {
			c.streamMu.Lock()
			defer c.streamMu.Unlock()
			if c.streamGen == gen {
				c.promptAppended = true
			}
		},
	}) {
		if !c.CurrentLoop(gen) {
			return
		}
		if err != nil {
			loopErr = err
			return
		}
		if ev != nil {
			// Partial model output remains ordinary transcript content; the error
			// itself is shown once by the completion dialog.
			if update, ok := ev.(session.AssistantMessageUpdate); ok && update.Message.State == session.StateError {
				update.Message.State = session.StateCancelled
				ev = update
			}
			c.publish(SessionEventMsg{Event: ev, Gen: gen})
		}
	}
}
