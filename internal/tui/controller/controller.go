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
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/project/model"
	"github.com/pulseaiclub/phi/internal/session"
)

const defaultAskTimeoutSec = 120

// EngineController owns agent.Engine lifecycle and stream cancellation.
// It talks to the UI only by publishing Msg values onto the Bus.
type EngineController struct {
	engine *agent.Engine
	proj   *project.Project

	streamMu       sync.Mutex
	streamCancel   context.CancelFunc
	streamGen      int
	lastPrompt     LastPrompt
	promptAppended bool
	retryReady     bool

	bus *Bus

	sessionDir string
	cwd        string
	modelMu    sync.RWMutex // Protect engine/model snapshots read by background jobs and local bash.
	modelCfg   llm.ModelConfig
	// roleModels maps sub-agent role → model name. Empty → inherit modelCfg.
	roleModels map[job.Role]string
	jobs       *job.Manager
	unsubJobs  func()

	askTimeoutSec int
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

	c := &EngineController{
		bus:           bus,
		proj:          proj,
		cwd:           proj.Root(),
		sessionDir:    proj.SessionDir(),
		askTimeoutSec: defaultAskTimeoutSec,
		modelCfg:      config.Model(),
		roleModels:    config.Agents.Models.RoleOverrides(),
	}
	c.agentsEnabled.Store(config.Agents.Enabled)

	sess, err := c.openSession(c.modelCfg, "")
	if err != nil {
		return nil, err
	}

	jobs, err := agent.NewJobManager(proj.JobsDir(), c.modelCfg, c.modelForRole, c.Extensions)
	if err != nil {
		return nil, err
	}
	c.jobs = jobs

	if pool, err := mcp.LoadPool(proj.MCPConfigFile()); err != nil {
		debuglog.Logf("mcp: load: %v", err)
	} else {
		c.mcpPool = pool
	}

	r := c.loadSessionExtensions(sess)
	eng, err := c.newEngine(c.modelCfg, r, sess)
	if err != nil {
		r.Close()
		_ = jobs.Close(context.Background())
		if c.mcpPool != nil {
			_ = c.mcpPool.Close()
		}
		return nil, err
	}
	c.extRunner.Store(r)
	c.engine = eng
	c.startJobProgress()
	c.emitSessionStart("startup", "")
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

func (c *EngineController) SetAgentsEnabled(v bool) error {
	sess, modelCfg, r := c.engine.Session(), c.modelSnapshot(), c.Extensions()
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
	c.installEngine(eng, modelCfg)
	return nil
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
	sess := c.engine.Session()
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
	c.installEngine(eng, modelCfg)
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

// Startup and resume are fail-open; an explicit reload reports discovery errors.
func (c *EngineController) loadSessionExtensions(sess *agent.Session) *extension.Runner {
	r, _, err := c.loadExtensions(sess)
	if err != nil {
		debuglog.Logf("extension: load failed: %v", err)
	}
	return r
}

func (c *EngineController) loadExtensions(sess *agent.Session) (*extension.Runner, []loader.Warning, error) {
	host := ext.HostOpts{
		UI: extension.BusUI{
			NotifyFn: c.handleExtNotify, SetStatusFn: c.handleExtStatus, ConfirmFn: c.askExtConfirm,
		},
		Cwd: sess.Cwd(), SessionID: sess.ID(), HasUI: true,
		RefreshTools: c.requestTopologyRefresh,
	}
	base := agent.EngineConfig{Jobs: c.engineJobs(), MCP: c.mcpPool}
	r, warns, err := extension.LoadWithHost(
		c.proj.Global().ExtensionsDir(),
		c.proj.ExtensionsDir(),
		extension.HostConfig{
			Host: host, BaseTools: base.CoreTools(sess), OnChange: c.requestTopologyRefresh,
			OnPrompt: func(r *extension.Runner, sessionID, text string) {
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
	if m.runner != nil && m.runner == c.Extensions() && c.engine != nil && m.sessionID == c.SessionID() {
		c.StartPrompt(m.text, nil, nil)
	}
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

// SyncExtensionTools replaces the engine for future turns on the UI goroutine.
func (c *EngineController) SyncExtensionTools() error {
	if c.engine == nil {
		return nil
	}
	modelCfg := c.modelSnapshot()
	eng, err := c.newEngine(modelCfg, c.Extensions(), c.engine.Session())
	if err != nil {
		return err
	}
	c.installEngine(eng, modelCfg)
	return nil
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
	sess := c.engine.Session()
	r, _, loadErr := c.loadExtensions(sess)
	if loadErr != nil {
		debuglog.Logf("extension: reload on SetModel: %v", loadErr)
		r = c.Extensions()
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
	c.installEngine(eng, cfg)
	return nil
}

// SetThinkLevel changes the thinking level for the current session.
func (c *EngineController) SetThinkLevel(mode llm.ThinkMode) error {
	cfg := c.modelSnapshot()
	cfg.Think = llm.ThinkConfig{Mode: mode, Enabled: mode != llm.Off}
	if err := model.ValidateThinking(cfg); err != nil {
		return err
	}
	eng, err := c.newEngine(cfg, c.Extensions(), c.engine.Session())
	if err != nil {
		return err
	}
	c.installEngine(eng, cfg)
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

func (c *EngineController) modelSnapshot() llm.ModelConfig {
	c.modelMu.RLock()
	defer c.modelMu.RUnlock()
	return c.modelCfg
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
	c.modelMu.RLock()
	engine := c.engine
	c.modelMu.RUnlock()
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

	sess, err := c.openSession(cfg, id)
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
	if sessCwd := eng.SessionCwd(); sessCwd != "" && c.cwd != "" && sessCwd != c.cwd {
		cwdWarning = fmt.Sprintf("cwd %s ≠ %s", filepath.Base(sessCwd), filepath.Base(c.cwd))
	}
	c.activateSession(eng, cfg, "resume", prevID)
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
	sess, err := c.openSession(cfg, "")
	if err != nil {
		return err
	}
	engine, err := c.newEngine(cfg, c.Extensions(), sess)
	if err != nil {
		return err
	}
	c.commitSessionSwitch("new", "")
	c.switchRunnerSession(sess)
	c.activateSession(engine, cfg, "new", prevID)
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
	eng, err := c.newEngine(cfg, c.Extensions(), forked)
	if err != nil {
		c.discardFork(forked.ID())
		return "", err
	}
	c.commitSessionSwitch("fork", forked.ID())
	c.switchRunnerSession(forked)
	c.activateSession(eng, cfg, "fork", prevID)
	return forked.ID(), nil
}

// activateSession publishes the new session only after installing its engine and model together.
func (c *EngineController) activateSession(eng *agent.Engine, cfg llm.ModelConfig, reason, prevID string) {
	c.installEngine(eng, cfg)
	c.emitSessionStart(reason, prevID)
}

func (c *EngineController) installEngine(eng *agent.Engine, cfg llm.ModelConfig) {
	c.modelMu.Lock()
	defer c.modelMu.Unlock()
	c.engine = eng
	c.modelCfg = cfg
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

func (c *EngineController) resolveModel() (llm.ModelConfig, error) {
	if cfg := c.modelSnapshot(); cfg.Name != "" {
		return cfg, nil
	}
	if err := c.proj.LoadConfig(); err != nil {
		return llm.ModelConfig{}, err
	}
	return c.proj.Config().Model(), nil
}

func (c *EngineController) switchRunnerSession(sess *agent.Session) {
	r := c.Extensions()
	base := agent.EngineConfig{Jobs: c.engineJobs(), MCP: c.mcpPool}
	r.SetBaseTools(base.CoreTools(sess))
	r.SetMeta(sess.ID(), sess.Cwd())
}

func (c *EngineController) openSession(cfg llm.ModelConfig, resumeID string) (*agent.Session, error) {
	opts := []agent.SessionOption{
		agent.WithCwd(c.cwd),
		agent.WithSessionDir(c.sessionDir),
		agent.WithPersist(true),
		agent.WithModel(cfg.Name),
	}
	if resumeID != "" {
		opts = append(opts, agent.WithResumeID(resumeID))
	}
	return agent.NewSession(opts...)
}

// newEngine binds a session to a fresh engine on the controller-wide gate,
// jobs, extensions, MCP pool and hooks. Every session switch funnels through
// here, so a forked session runs on exactly the wiring of a resumed one.
func (c *EngineController) newEngine(
	cfg llm.ModelConfig,
	extRunner *extension.Runner,
	sess *agent.Session,
) (*agent.Engine, error) {
	return c.engineConfig(cfg, extRunner).NewEngine(cfg, sess)
}

func (c *EngineController) engineConfig(cfg llm.ModelConfig, r *extension.Runner) agent.EngineConfig {
	return agent.EngineConfig{Jobs: c.engineJobs(), Extensions: r, MCP: c.mcpPool, Hooks: model.HooksForConfig(cfg)}
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
	c.publish(LoopStartedMsg{Gen: c.streamGen})
	go c.runLoop(ctx, c.streamGen, c.engine, c.lastPrompt, c.promptAppended)
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
	r := c.Extensions()
	if r == nil {
		return ext.SessionEffects{}
	}
	return r.EmitSessionBeforeSwitch(ext.SessionBeforeSwitchEvent{
		Reason:          reason,
		TargetSessionID: targetID,
	})
}

func (c *EngineController) sessionShutdown(reason, targetID string) {
	r := c.Extensions()
	if r == nil {
		return
	}
	c.publishSessionEffects(r.EmitSessionShutdown(ext.SessionShutdownEvent{
		Reason:          reason,
		TargetSessionID: targetID,
	}))
}

func (c *EngineController) emitSessionStart(reason, previousID string) {
	r := c.Extensions()
	if r == nil {
		return
	}
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
