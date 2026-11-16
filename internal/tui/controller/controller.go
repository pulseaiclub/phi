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

	"github.com/pulseaiclub/phi/internal/extension/loader"

	ext "github.com/pulseaiclub/phi/ext/go"
	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/debuglog"
	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/project/model"
	"github.com/pulseaiclub/phi/internal/session"
)

const defaultAskTimeoutSec = 120

// EngineController owns agent.Engine lifecycle and stream cancellation.
// It talks to the UI only by publishing Msg values onto the Bus.
type EngineController struct {
	engineMu sync.RWMutex // guards engine against configurator updates vs loop start
	engine   *agent.Engine
	proj     *project.Project
	// configurator is the single entry point for engine configuration changes,
	// mirroring the OpenTelemetry MeterConfiguratorHandle pattern.
	configurator *agent.EngineConfigurator

	streamMu       sync.Mutex
	streamCancel   context.CancelFunc
	streamGen      int
	lastPrompt     LastPrompt
	promptAppended bool
	retryReady     bool

	bus *Bus

	sessionDir string
	cwd        string
	// roleModels maps sub-agent role → model name. Empty → inherit the active model.
	roleModels map[job.Role]string
	jobs       *job.Manager
	unsubJobs  func()

	gate          permission.Gate
	askTimeoutSec int
	allowAll      atomic.Bool
	agentsEnabled atomic.Bool
	extRunner     atomic.Pointer[extension.Runner]
	mcpPool       *mcp.Pool

	lastJobProgress sync.Map // progressSlot → progressSig
}

// NewController returns a live EngineController. proj must be non-nil.
// Failure returns (nil, err), never a half-initialized value.
func NewController(bus *Bus, proj *project.Project) (*EngineController, error) {
	if err := proj.LoadConfig(); err != nil {
		return nil, err
	}
	config := proj.Config()
	configurator := agent.NewEngineConfigurator()
	c := &EngineController{
		bus:          bus,
		proj:         proj,
		cwd:          proj.Root(),
		sessionDir:   proj.SessionDir(),
		roleModels:   config.Agents.Models.RoleOverrides(),
		configurator: configurator,
	}
	if err := configurator.Register(c.applyEngineSpec); err != nil {
		return nil, err
	}
	// TUI defaults to bypass; palette → settings → permissions toggles it.
	c.allowAll.Store(true)
	c.gate, c.askTimeoutSec = newGate(config.Permissions, &c.allowAll)
	c.agentsEnabled.Store(config.Agents.Enabled)

	extRunner := c.loadExtensionsFailOpen()
	c.extRunner.Store(extRunner)

	jobs, err := agent.NewJobManager(proj.JobsDir(), config.Model(),
		agent.WithJobsModelFunc(c.modelForRole),
		agent.WithJobsExtensionsFunc(c.Extensions),
	)
	if err != nil {
		return nil, err
	}
	c.jobs = jobs

	if pool, err := mcp.LoadPool(proj.MCPConfigFile()); err != nil {
		debuglog.Logf("mcp: load: %v", err)
	} else {
		c.mcpPool = pool
	}

	spec, err := c.openSessionSpec(config.Model(), extRunner, "")
	if err != nil {
		return nil, err
	}
	c.setEngineSpec(spec)
	eng := c.currentEngine()
	if eng == nil {
		return nil, errors.New("agent: engine not installed after configuration")
	}
	c.startJobProgress()
	c.emitSessionStart("startup", eng.SessionID(), "")
	return c, nil
}

func (c *EngineController) startJobProgress() {
	ch, cancel := c.jobs.Subscribe()
	c.unsubJobs = cancel
	go func() {
		for p := range ch {
			if c.shouldPublishJobProgress(p) {
				c.publish(JobProgressMsg{Progress: p})
			}
		}
	}()
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

// newGate builds the permission gate and ask timeout from policy. allowAll is
// shared with the controller so palette toggles survive later gate rebuilds.
func newGate(policy permission.Policy, allowAll *atomic.Bool) (permission.Gate, int) {
	askTimeoutSec := defaultAskTimeoutSec
	if policy.AskTimeoutSec > 0 {
		askTimeoutSec = policy.AskTimeoutSec
	}
	if policy.Mode == "" {
		policy.Mode = permission.ModeInteractive
	}
	if policy.Mode == permission.ModeAllowAll {
		allowAll.Store(true)
	}
	// Do not clear allowAll when config does not pin a mode — the TUI defaults
	// to bypass, and the palette toggle must survive SetModel / gate rebuilds.
	var inner permission.Gate
	inner, err := permission.NewGate(policy, permission.WorkspaceRoot())
	if err != nil {
		inner, err = permission.NewGate(permission.DefaultPolicy(), permission.WorkspaceRoot())
	}
	if err != nil {
		inner = permission.AllowAll{}
	}
	return &permission.BypassGate{Inner: inner, Enabled: allowAll}, askTimeoutSec
}

func (c *EngineController) SetAllowAll(v bool) {
	c.allowAll.Store(v)
}

// AllowAll reports whether the permission gate is in bypass mode.
func (c *EngineController) AllowAll() bool { return c.allowAll.Load() }

func (c *EngineController) SetAgentsEnabled(v bool) {
	c.agentsEnabled.Store(v)
	c.applyCurrentConfig("agents")
}

// AgentsEnabled reports whether sub-agent (agent_*) tools are registered.
func (c *EngineController) AgentsEnabled() bool { return c.agentsEnabled.Load() }

func (c *EngineController) modelForRole(role job.Role) llm.ModelConfig {
	role = job.NormalizeRole(string(role))
	name := c.roleModels[role]
	if name == "" {
		return c.modelSnapshot()
	}
	if route, ok := c.Extensions().LookupModel(name); ok {
		m, err := extensionModelConfig(route, c.proj.Config())
		if err != nil {
			debuglog.Logf("agents: extension role model %q: %v; using parent", name, err)
			return c.modelSnapshot()
		}
		return m
	}
	if m, ok := c.proj.Config().FindModel(name); ok {
		return m
	}
	debuglog.Logf("agents: unknown role model %q for %s; using parent", name, role)
	return c.modelSnapshot()
}

// RoleModel returns the session-only model name for a sub-agent role.
// Empty means the role inherits the parent model.
func (c *EngineController) RoleModel(role string) string {
	r, err := job.ParseRole(role)
	if err != nil {
		return ""
	}
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
	if name == "" {
		delete(c.roleModels, r)
		return nil
	}
	if _, ok := c.proj.Config().FindModel(name); !ok {
		if _, ok := c.Extensions().LookupModel(name); !ok {
			return fmt.Errorf("unknown model %q", name)
		}
	}
	c.roleModels[r] = name
	return nil
}

func (c *EngineController) engineJobs() *job.Manager {
	if !c.agentsEnabled.Load() {
		return nil
	}
	return c.jobs
}

func (c *EngineController) Extensions() *extension.Runner {
	return c.extRunner.Load()
}

func (c *EngineController) ReloadExtensions() (loaded int, warns []loader.Warning, err error) {
	r, warns, err := c.loadExtensionRunner()
	if err != nil {
		return 0, warns, err
	}
	logExtensionWarnings(warns)
	c.swapExtensionRunner(r)
	c.setEngineSpec(c.currentEngineSpec())
	if r == nil {
		return 0, warns, nil
	}
	return len(r.Loaded()), warns, nil
}

func (c *EngineController) swapExtensionRunner(r *extension.Runner) {
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
	return r.ReloadExtension(name)
}

// UnloadExtension unloads a single extension by name.
func (c *EngineController) UnloadExtension(name string) error {
	r := c.extRunner.Load()
	if r == nil {
		return errors.New("no extension runner loaded")
	}
	return r.UnloadExtension(name)
}

// ListExtensionStates returns all extension states.
func (c *EngineController) ListExtensionStates() map[string]extension.FiberState {
	r := c.extRunner.Load()
	if r == nil {
		return nil
	}
	return r.ListExtensions()
}

// loadExtensionRunner discovers ~/.phi/extensions and <cwd>/.phi/extensions
// with host capabilities baked into the runner at construction, so fibers
// never come up unwired.
func (c *EngineController) loadExtensionRunner() (*extension.Runner, []loader.Warning, error) {
	cwd, sessionID := c.hostSessionInfo()
	host := ext.HostOpts{
		UI: extension.BusUI{
			NotifyFn:    c.handleExtNotify,
			SetStatusFn: c.handleExtStatus,
			ConfirmFn:   c.askExtConfirm,
		},
		Cwd:       cwd,
		SessionID: sessionID,
		HasUI:     true,
		// RefreshTools is called by an extension, on its own reader goroutine.
		// Rebinding the engine's tools from there would race the agent loop, so
		// it takes the same route as any other topology change: a message the
		// UI goroutine applies.
		RefreshTools:    c.requestTopologyRefresh,
		SendUserMessage: c.sendUserMessage,
	}
	return extension.Load(c.proj.Global().ExtensionsDir(), c.proj.ExtensionsDir(),
		extension.WithHost(host),
		// Topology changes (extension-initiated reload/unload) happen off the
		// UI goroutine, so they arrive as a message instead of a direct call.
		extension.WithOnChange(c.requestTopologyRefresh),
	)
}

// loadExtensionsFailOpen discovers extensions, treating load errors as
// non-fatal (fail-open: no extensions).
func (c *EngineController) loadExtensionsFailOpen() *extension.Runner {
	r, warns, err := c.loadExtensionRunner()
	if err != nil {
		debuglog.Logf("extension: load failed: %v", err)
		return nil
	}
	logExtensionWarnings(warns)
	return r
}

func logExtensionWarnings(warns []loader.Warning) {
	for _, w := range warns {
		debuglog.Logf("extension: %s", w.String())
	}
}

func (c *EngineController) hostSessionInfo() (cwd, sessionID string) {
	if c.engine != nil {
		return c.engine.SessionCwd(), c.engine.SessionID()
	}
	// Construction bakes the host in before the engine exists.
	return c.proj.Root(), ""
}

func (c *EngineController) handleExtNotify(message, kind string) {
	toastKind := toast.ToastSuccess
	switch strings.ToLower(kind) {
	case "warning":
		toastKind = toast.ToastWarning
	case "error":
		toastKind = toast.ToastError
	}
	c.publish(ToastMsg{Message: message, Kind: toastKind, Duration: 3 * time.Second})
}

func (c *EngineController) handleExtStatus(_, text string) {
	c.publish(ExtSessionEffectsMsg{Status: text, StatusSet: true})
}

func (c *EngineController) requestTopologyRefresh() {
	c.publish(ExtTopologyChangedMsg{})
}

func (c *EngineController) sendUserMessage(text string) {
	go c.StartPrompt(text, nil, nil)
}

// SyncExtensionTools rebuilds the engine from the current extension snapshot.
// Call it on the UI goroutine after an extension topology change.
func (c *EngineController) SyncExtensionTools() {
	c.applyCurrentConfig("extensions")
}

// applyCurrentConfig re-publishes the controller's current engine spec through
// the configurator. Build failures keep the previous engine; applyEngineSpec
// logs the error.
func (c *EngineController) applyCurrentConfig(_ string) {
	if c.engine == nil {
		return
	}
	c.setEngineSpec(c.currentEngineSpec())
}

// currentEngineSpec captures the controller's current configuration as one
// complete spec value.
func (c *EngineController) currentEngineSpec() agent.EngineSpec {
	return agent.EngineSpec{
		Model:      c.modelSnapshot(),
		Session:    c.engine.Session(),
		Extensions: c.Extensions(),
	}
}

// currentEngine reads the installed engine under the configurator's lock
// discipline (engineMu).
func (c *EngineController) currentEngine() *agent.Engine {
	c.engineMu.RLock()
	defer c.engineMu.RUnlock()
	return c.engine
}

func (c *EngineController) askTimeout() time.Duration {
	if c.askTimeoutSec > 0 {
		return time.Duration(c.askTimeoutSec) * time.Second
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

func (c *EngineController) askPermission(
	ctx context.Context,
	req permission.Request,
	reason string,
) (permission.AskResult, error) {
	if c.allowAll.Load() {
		return permission.AskResult{Approved: true}, nil
	}
	reply := make(chan AskReply, 1)
	c.publish(OverlayMsg{Kind: OverlayPermissionAsk, Request: req, Reason: reason, PermReply: reply})
	r, err := waitReply(ctx, reply, c.askTimeout(), func() {
		c.publish(OverlayMsg{Kind: OverlayPermissionDismiss})
	})
	if err != nil {
		return permission.AskResult{}, err
	}
	if r.AllowSession || r.AllowPersistent {
		c.allowAll.Store(true)
	}
	if r.AllowPersistent {
		_ = project.SetPermissionMode(c.proj.Global(), permission.ModeAllowAll)
	}
	return permission.AskResult{Approved: r.Approved, Feedback: r.Feedback}, nil
}

func (c *EngineController) askExtConfirm(req ext.ConfirmRequest) ext.ConfirmReply {
	reply := make(chan ExtConfirmReply, 1)
	c.publish(OverlayMsg{
		Kind:         OverlayExtConfirm,
		Title:        req.Title,
		Message:      req.Message,
		Yes:          req.Yes,
		No:           req.No,
		Danger:       req.Danger,
		ConfirmReply: reply,
	})
	r, _ := waitReply(context.Background(), reply, c.askTimeout(), func() {
		c.publish(OverlayMsg{Kind: OverlayExtConfirmDismiss})
	})
	return ext.ConfirmReply{OK: r.OK}
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
	c.gate, c.askTimeoutSec = newGate(c.proj.Config().Permissions, &c.allowAll)
	if _, _, err := c.ReloadExtensions(); err != nil {
		debuglog.Logf("extension: reload on SetModel: %v", err)
	}
	sess := c.engine.Session()
	if err := sess.SetModel(name); err != nil {
		return err
	}
	c.setEngineSpec(agent.EngineSpec{Model: cfg, Session: sess, Extensions: c.Extensions()})
	return nil
}

// SetThinkLevel changes the thinking level for the current session. The new
// model config travels inside the spec — never through a side channel.
func (c *EngineController) SetThinkLevel(mode llm.ThinkMode) error {
	cfg := c.modelSnapshot()
	cfg.Think = llm.ThinkConfig{Mode: mode, Enabled: mode != llm.Off}
	if err := model.ValidateThinking(cfg); err != nil {
		return err
	}
	spec := c.currentEngineSpec()
	spec.Model = cfg
	c.setEngineSpec(spec)
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

// modelSnapshot reads the active model from the configurator's latest spec —
// the single source of truth; the controller keeps no copy. A zero controller
// (partial wiring in tests) has no handle and reports an empty model.
func (c *EngineController) modelSnapshot() llm.ModelConfig {
	if c.configurator == nil {
		return llm.ModelConfig{}
	}
	return c.configurator.Snapshot().Model
}

func (c *EngineController) ImageEnabled() bool {
	return c.modelSnapshot().ImageEnabled
}

func (c *EngineController) SessionID() string {
	return c.engine.SessionID()
}

// AppendUserBash records a user "!!cmd" run in the current session so the next
// turn includes its output. sessionID guards a session switch that happened
// while the command was running: the output belongs to the old session, not
// whichever one is open now.
func (c *EngineController) AppendUserBash(sessionID, command, output string, exitCode int, cancelled bool) error {
	engine := c.engine
	if sessionID != "" && engine.SessionID() != sessionID {
		return nil
	}
	return engine.Session().AppendBash(command, output, exitCode, cancelled)
}

func (c *EngineController) SessionDir() string {
	return c.sessionDir
}

func (c *EngineController) SessionFile() string {
	return c.engine.SessionFile()
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

	extRunner := c.loadExtensionsFailOpen()
	spec, err := c.openSessionSpec(cfg, extRunner, id)
	if err != nil {
		return "", err
	}
	c.commitSessionSwitch("resume", prevID, id)
	c.swapExtensionRunner(extRunner)
	c.setEngineSpec(spec)
	eng := c.currentEngine()
	if eng == nil {
		return "", errors.New("agent: engine not installed after resume")
	}
	if sessCwd := eng.SessionCwd(); sessCwd != "" && c.cwd != "" && sessCwd != c.cwd {
		cwdWarning = fmt.Sprintf("cwd %s ≠ %s", filepath.Base(sessCwd), filepath.Base(c.cwd))
	}
	c.emitSessionStart("resume", eng.SessionID(), prevID)
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
	spec, err := c.openSessionSpec(cfg, c.Extensions(), "")
	if err != nil {
		return err
	}
	c.commitSessionSwitch("new", prevID, "")
	c.setEngineSpec(spec)
	eng := c.currentEngine()
	if eng == nil {
		return errors.New("agent: engine not installed after new session")
	}
	c.emitSessionStart("new", eng.SessionID(), prevID)
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
	forked, err := c.engine.Session().Fork()
	if err != nil {
		return "", err
	}
	cfg, err := c.resolveModel()
	if err != nil {
		c.discardFork(forked.ID())
		return "", err
	}
	c.setEngineSpec(agent.EngineSpec{Model: cfg, Session: forked, Extensions: c.Extensions()})
	eng := c.currentEngine()
	if eng == nil {
		c.discardFork(forked.ID())
		return "", errors.New("agent: engine not installed after fork")
	}
	c.commitSessionSwitch("fork", prevID, forked.ID())
	c.emitSessionStart("fork", eng.SessionID(), prevID)
	return forked.ID(), nil
}

// applyEngineSpec is the configurator's sole update callback, registered once
// at construction. It builds a complete engine for the new spec and installs
// it under engineMu, so a loop that already started keeps its old engine while
// a new loop starts against the new one — both fully formed. A build failure
// keeps the previous engine; the caller logs the error.
func (c *EngineController) applyEngineSpec(spec agent.EngineSpec) {
	eng, err := c.newEngine(spec.Model, spec.Extensions, spec.Session)
	if err != nil {
		debuglog.Logf("agent: apply engine spec: %v", err)
		return
	}
	c.engineMu.Lock()
	c.engine = eng
	c.engineMu.Unlock()
}

// setEngineSpec publishes a complete engine configuration through the
// configurator handle, mirroring OpenTelemetry's Handle.Set.
func (c *EngineController) setEngineSpec(spec agent.EngineSpec) {
	c.configurator.Set(spec)
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
	out := c.sessionBeforeSwitch(reason, prevID, targetID)
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
func (c *EngineController) commitSessionSwitch(reason, prevID, targetID string) {
	c.Cancel()
	c.sessionShutdown(reason, prevID, targetID)
}

func (c *EngineController) resolveModel() (llm.ModelConfig, error) {
	if cfg := c.modelSnapshot(); cfg.Name != "" {
		return cfg, nil
	}
	if err := c.proj.LoadConfig(); err != nil {
		return llm.ModelConfig{}, err
	}
	return c.proj.Config().Model(), nil
}

// openSessionSpec opens (or resumes) a session and resolves its model,
// returning the complete engine spec for the configurator to apply.
func (c *EngineController) openSessionSpec(
	cfg llm.ModelConfig,
	extRunner *extension.Runner,
	resumeID string,
) (agent.EngineSpec, error) {
	opts := []agent.SessionOption{
		agent.WithCwd(c.cwd),
		agent.WithSessionDir(c.sessionDir),
		agent.WithPersist(true),
		agent.WithModel(cfg.Name),
	}
	if resumeID != "" {
		opts = append(opts, agent.WithResumeID(resumeID))
	}
	sess, err := agent.NewSession(opts...)
	if err != nil {
		return agent.EngineSpec{}, err
	}
	if name := sess.ModelName(); resumeID != "" && name != "" {
		cfg, err = resolveModelConfig(c.proj.Config(), extRunner, name)
		if err != nil {
			return agent.EngineSpec{}, fmt.Errorf("restore session model %q: %w", name, err)
		}
	}
	return agent.EngineSpec{Model: cfg, Session: sess, Extensions: extRunner}, nil
}

// newEngine binds a session to a fresh engine on the controller-wide gate,
// jobs, extensions, MCP pool and hooks. Every session switch funnels through
// here, so a forked session runs on exactly the wiring of a resumed one.
func (c *EngineController) newEngine(
	cfg llm.ModelConfig,
	extRunner *extension.Runner,
	sess *agent.Session,
) (*agent.Engine, error) {
	eng, err := agent.NewEngine(cfg, sess,
		agent.WithGate(c.gate),
		agent.WithAsk(c.askPermission),
		agent.WithJobs(c.engineJobs()),
		agent.WithExtensions(extRunner),
		agent.WithMCP(c.mcpPool),
		agent.WithHooks(model.HooksForConfig(cfg)),
		agent.WithConfigurator(c.configurator),
	)
	if err != nil {
		return nil, err
	}
	if extRunner != nil {
		extRunner.SetBaseTools(eng.BaseTools())
		extRunner.SetMeta(eng.SessionID(), eng.SessionCwd())
	}
	return eng, nil
}

func (c *EngineController) ReplaySnapshot() session.Snapshot {
	return session.ReplaySnapshot(c.engine.Session().PathEntries(), c.engine.ToolDetail)
}

func (c *EngineController) StartPrompt(text string, pendingSkills []string, images []llm.Image) {
	c.streamMu.Lock()
	defer c.streamMu.Unlock()
	c.lastPrompt = LastPrompt{
		Text:   text,
		Skills: append([]string(nil), pendingSkills...),
		Images: append([]llm.Image(nil), images...),
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
	c.engineMu.RLock()
	eng := c.engine
	c.engineMu.RUnlock()
	c.publish(LoopStartedMsg{Gen: c.streamGen})
	go c.runLoop(ctx, c.streamGen, eng, c.lastPrompt, c.promptAppended)
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
	c.sessionShutdown("quit", c.SessionID(), "")
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

func (c *EngineController) sessionBeforeSwitch(reason, fromID, targetID string) ext.SessionEffects {
	r := c.Extensions()
	if r == nil {
		return ext.SessionEffects{}
	}
	r.SetMeta(fromID, c.cwd)
	return r.EmitSessionBeforeSwitch(ext.SessionBeforeSwitchEvent{
		Reason:          reason,
		TargetSessionID: targetID,
	})
}

func (c *EngineController) sessionShutdown(reason, sessionID, targetID string) {
	r := c.Extensions()
	if r == nil {
		return
	}
	r.SetMeta(sessionID, c.cwd)
	c.publishSessionEffects(r.EmitSessionShutdown(ext.SessionShutdownEvent{
		Reason:          reason,
		TargetSessionID: targetID,
	}))
}

func (c *EngineController) emitSessionStart(reason, sessionID, previousID string) {
	r := c.Extensions()
	if r == nil {
		return
	}
	r.SetMeta(sessionID, c.cwd)
	c.publishSessionEffects(r.EmitSessionStart(ext.SessionStartEvent{
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
