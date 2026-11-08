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
	modelMu    sync.RWMutex // Protect model snapshots read by background jobs.
	modelCfg   llm.ModelConfig
	// roleModels maps sub-agent role → model name. Empty → inherit modelCfg.
	roleModels map[job.Role]string
	jobs       *job.Manager
	unsubJobs  func()

	gate          permission.Gate
	askTimeoutSec int
	allowAll      atomic.Bool
	agentsEnabled atomic.Bool
	extRunner     atomic.Pointer[extension.Runner]
	mcpPool       *mcp.Pool

	lastJobProgress sync.Map // job slot key → last published signature
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
		roleModels:    make(map[job.Role]string),
	}
	// TUI defaults to bypass; palette → settings → permissions toggles it.
	c.allowAll.Store(true)

	c.initGate(config.Permissions)
	c.agentsEnabled.Store(config.Agents.Enabled)
	if s := config.Agents.Models.Explore; s != "" {
		c.roleModels[job.RoleExplore] = s
	}
	if s := config.Agents.Models.Review; s != "" {
		c.roleModels[job.RoleReview] = s
	}
	if s := config.Agents.Models.Worker; s != "" {
		c.roleModels[job.RoleWorker] = s
	}

	extRunner := loadExtensions(proj)
	c.extRunner.Store(extRunner)
	c.bindExtensionHost(extRunner)

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

	eng, _, err := c.openEngine(c.modelCfg, extRunner, "")
	if err != nil {
		return nil, err
	}
	c.engine = eng
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

func (c *EngineController) shouldPublishJobProgress(p job.Progress) bool {
	key := p.JobID + "\x00" + p.ToolUseID
	if p.ToolUseID == "" {
		key = p.JobID + "\x00" + p.Name + "\x00" + p.Detail
	}
	sig := p.Status + "\x00" + p.Name + "\x00" + p.Detail
	if prev, ok := c.lastJobProgress.Load(key); ok && prev.(string) == sig {
		return false
	}
	c.lastJobProgress.Store(key, sig)
	return true
}

func (c *EngineController) initGate(policy permission.Policy) {
	if policy.AskTimeoutSec > 0 {
		c.askTimeoutSec = policy.AskTimeoutSec
	}
	if policy.Mode == "" {
		policy.Mode = permission.ModeInteractive
	}
	if policy.Mode == permission.ModeAllowAll {
		c.allowAll.Store(true)
	}
	// Do not clear allowAll when config does not pin a mode — the TUI defaults
	// to bypass, and the palette toggle must survive SetModel / re-init.
	var inner permission.Gate
	inner, err := permission.NewGate(policy, permission.WorkspaceRoot())
	if err != nil {
		inner, err = permission.NewGate(permission.DefaultPolicy(), permission.WorkspaceRoot())
	}
	if err != nil {
		inner = permission.AllowAll{}
	}
	c.gate = &permission.BypassGate{Inner: inner, Enabled: &c.allowAll}
}

func (c *EngineController) SetAllowAll(v bool) {
	c.allowAll.Store(v)
}

// AllowAll reports whether the permission gate is in bypass mode.
func (c *EngineController) AllowAll() bool { return c.allowAll.Load() }

func (c *EngineController) SetAgentsEnabled(v bool) {
	c.agentsEnabled.Store(v)
	c.engine.SetJobs(c.engineJobs())
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
		m, err := extensionModelConfig(route, c.proj.Config(), c.MaxRounds())
		if err != nil {
			debuglog.Logf("agents: extension role model %q: %v; using parent", name, err)
			return c.modelSnapshot()
		}
		return m
	}
	if m, ok := c.proj.Config().FindModel(name); ok {
		m.MaxRounds = c.MaxRounds()
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
	r, warns, err := extension.Load(c.proj.Global().ExtensionsDir(), c.proj.ExtensionsDir())
	if err != nil {
		return 0, warns, err
	}
	logExtensionWarnings(warns)
	c.swapExtensionRunner(r)
	c.engine.SetExtensions(r)
	if r == nil {
		return 0, warns, nil
	}
	return len(r.Loaded()), warns, nil
}

func (c *EngineController) swapExtensionRunner(r *extension.Runner) {
	if prev := c.extRunner.Swap(r); prev != nil {
		prev.Close()
	}
	c.bindExtensionHost(r)
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

// loadExtensions discovers ~/.phi/extensions and <cwd>/.phi/extensions.
// Load errors are non-fatal (fail-open: no extensions).
func loadExtensions(proj *project.Project) *extension.Runner {
	r, warns, err := extension.Load(proj.Global().ExtensionsDir(), proj.ExtensionsDir())
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

func (c *EngineController) bindExtensionHost(r *extension.Runner) {
	if r == nil {
		return
	}
	cwd, sessionID := "", ""
	if c.engine != nil {
		cwd = c.engine.SessionCwd()
		sessionID = c.engine.SessionID()
	} else {
		// NewController binds the host before the engine exists.
		cwd = c.proj.Root()
	}
	r.Bind(ext.HostOpts{
		UI: extension.BusUI{
			NotifyFn: func(message, kind string) {
				toastKind := toast.ToastSuccess
				switch strings.ToLower(kind) {
				case "warning":
					toastKind = toast.ToastWarning
				case "error":
					toastKind = toast.ToastError
				}
				c.publish(ToastMsg{Message: message, Kind: toastKind, Duration: 3 * time.Second})
			},
			SetStatusFn: func(_, text string) {
				c.publish(ExtSessionEffectsMsg{Status: text, StatusSet: true})
			},
			ConfirmFn: c.askExtConfirm,
		},
		Cwd:       cwd,
		SessionID: sessionID,
		HasUI:     true,
		// RefreshTools is called by an extension, on its own reader goroutine.
		// Rebinding the engine's tools from there would race the agent loop, so
		// it takes the same route as any other topology change: a message the
		// UI goroutine applies.
		RefreshTools: func() { c.publish(ExtTopologyChangedMsg{}) },
		SendUserMessage: func(text string) {
			go c.StartPrompt(text, nil, nil)
		},
	})

	// Topology changes (extension-initiated reload/unload) happen off the
	// UI goroutine, so they arrive as a message instead of a direct call.
	r.SetOnChange(func() { c.publish(ExtTopologyChangedMsg{}) })
}

// SyncExtensionTools re-derives the agent's tool list from the current runner.
// Call it on the UI goroutine: it rebinds the engine's client and executor, so
// an extension reader goroutine must not.
func (c *EngineController) SyncExtensionTools() {
	if c.engine == nil {
		return
	}
	c.engine.SetExtensions(c.Extensions())
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
	c.initGate(c.proj.Config().Permissions)
	c.engine.SetPermission(c.gate, c.askPermission)
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
	prevID, err := c.beginSessionSwitch("resume", id, true)
	if err != nil {
		return "", err
	}
	cfg, err := c.resolveModel()
	if err != nil {
		return "", err
	}

	extRunner := loadExtensions(c.proj)
	c.swapExtensionRunner(extRunner)
	eng, cfg, err := c.openEngine(cfg, extRunner, id)
	if err != nil {
		return "", err
	}
	if sessCwd := eng.SessionCwd(); sessCwd != "" && c.cwd != "" && sessCwd != c.cwd {
		cwdWarning = fmt.Sprintf("cwd %s ≠ %s", filepath.Base(sessCwd), filepath.Base(c.cwd))
	}
	c.modelMu.Lock()
	c.engine = eng
	c.modelCfg = cfg
	c.modelMu.Unlock()
	c.emitSessionStart("resume", eng.SessionID(), prevID)
	return cwdWarning, nil
}

// NewSession starts a brand-new persisted session. Caller must ensure no agent
// stream / local bash is in flight.
func (c *EngineController) NewSession() error {
	prevID, err := c.beginSessionSwitch("new", "", true)
	if err != nil {
		return err
	}
	cfg, err := c.resolveModel()
	if err != nil {
		return err
	}
	engine, cfg, err := c.openEngine(cfg, c.Extensions(), "")
	if err != nil {
		return err
	}
	c.modelMu.Lock()
	c.engine = engine
	c.modelCfg = cfg
	c.modelMu.Unlock()
	c.emitSessionStart("new", engine.SessionID(), prevID)
	return nil
}

func (c *EngineController) beginSessionSwitch(reason, targetID string, cancelStream bool) (prevID string, err error) {
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
	if cancelStream {
		c.Cancel()
	}
	c.sessionShutdown(reason, prevID)
	return prevID, nil
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

func (c *EngineController) openEngine(
	cfg llm.ModelConfig,
	extRunner *extension.Runner,
	resumeID string,
) (*agent.Engine, llm.ModelConfig, error) {
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
		return nil, llm.ModelConfig{}, err
	}
	if name := sess.ModelName(); resumeID != "" && name != "" {
		cfg, err = resolveModelConfig(c.proj.Config(), extRunner, name)
		if err != nil {
			return nil, llm.ModelConfig{}, fmt.Errorf("restore session model %q: %w", name, err)
		}
		cfg.MaxRounds = c.MaxRounds()
	}
	eng, err := agent.NewEngine(cfg, sess,
		agent.WithGate(c.gate),
		agent.WithAsk(c.askPermission),
		agent.WithJobs(c.engineJobs()),
		agent.WithExtensions(extRunner),
		agent.WithMCP(c.mcpPool),
		agent.WithHooks(model.HooksForConfig(cfg)),
	)
	if err != nil {
		return nil, llm.ModelConfig{}, err
	}
	return eng, cfg, nil
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
	c.sessionShutdown("quit", c.SessionID())
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

func (c *EngineController) sessionShutdown(reason, sessionID string) {
	r := c.Extensions()
	if r == nil {
		return
	}
	r.SetMeta(sessionID, c.cwd)
	c.publishSessionEffects(r.EmitSessionShutdown(ext.SessionShutdownEvent{Reason: reason}))
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
