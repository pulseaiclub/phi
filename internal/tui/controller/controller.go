package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
	"github.com/pulseaiclub/phi/internal/session"
)

const defaultAskTimeoutSec = 120

// EngineController owns agent.Engine lifecycle and stream cancellation.
// It talks to the UI only by publishing Msg values onto the Bus.
type EngineController struct {
	engine *agent.Engine
	proj   *project.Project

	streamMu     sync.Mutex
	streamCancel context.CancelFunc
	streamGen    int

	bus *Bus

	sessionDir string
	cwd        string
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
func NewController(bus *Bus, proj *project.Project, cwd string) (*EngineController, error) {
	if bus == nil {
		return nil, errors.New("tui: nil bus")
	}
	if proj == nil {
		return nil, errors.New("tui: nil project")
	}
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

	eng, err := c.openEngine(c.modelCfg, extRunner, "")
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
	if policy.DangerouslyAllowAll {
		c.allowAll.Store(true)
	}
	// Do not clear allowAll when config omits dangerously_allow_all — TUI defaults
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

func (c *EngineController) SetAgentsEnabled(v bool) {
	c.agentsEnabled.Store(v)
	if c.engine != nil {
		c.engine.SetJobs(c.engineJobs())
	}
}

func (c *EngineController) modelForRole(role job.Role) llm.ModelConfig {
	role = job.NormalizeRole(string(role))
	if name := c.roleModels[role]; name != "" {
		if m, ok := c.proj.Config().FindModel(name); ok {
			return m
		}
		debuglog.Logf("agents: unknown role model %q for %s; using parent", name, role)
	}
	return c.modelCfg
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
		return fmt.Errorf("unknown model %q", name)
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

func (c *EngineController) ReloadExtensions() (loaded int, warns []extension.Warning, err error) {
	if c.proj == nil {
		return 0, nil, errors.New("project not available")
	}
	r, warns, err := extension.Load(c.proj.Global().ExtensionsDir(), c.proj.ExtensionsDir())
	if err != nil {
		return 0, warns, err
	}
	logExtensionWarnings(warns)
	c.swapExtensionRunner(r)
	if c.engine != nil {
		c.engine.SetExtensions(r)
	}
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

func (c *EngineController) ListExtensions() ([]extension.Discovered, []extension.Warning, error) {
	if c.proj == nil {
		return nil, nil, errors.New("project not available")
	}
	return extension.Discover(c.proj.Global().ExtensionsDir(), c.proj.ExtensionsDir())
}

// loadExtensions discovers ~/.phi/extensions and <cwd>/.phi/extensions.
// Load errors are non-fatal (fail-open: no extensions).
func loadExtensions(proj *project.Project) *extension.Runner {
	if proj == nil {
		return nil
	}
	r, warns, err := extension.Load(proj.Global().ExtensionsDir(), proj.ExtensionsDir())
	if err != nil {
		debuglog.Logf("extension: load failed: %v", err)
		return nil
	}
	logExtensionWarnings(warns)
	return r
}

func logExtensionWarnings(warns []extension.Warning) {
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
	} else if c.proj != nil {
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
		RefreshTools: func() {
			if c.engine != nil {
				c.engine.SetExtensions(r)
			}
		},
		SendUserMessage: func(text string) {
			go c.StartPrompt(text, nil, nil)
		},
	})
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
	if r.AllowPersistent && c.proj != nil {
		_ = project.SetDangerouslyAllowAll(c.proj.Global(), true)
	}
	return permission.AskResult{Approved: r.Approved, Feedback: r.Feedback}, nil
}

func (c *EngineController) askContinue(ctx context.Context, maxRounds int) (bool, error) {
	reply := make(chan ContinueReply, 1)
	c.publish(OverlayMsg{Kind: OverlayContinueAsk, MaxRounds: maxRounds, ContReply: reply})
	r, err := waitReply(ctx, reply, c.askTimeout(), func() {
		c.publish(OverlayMsg{Kind: OverlayContinueDismiss})
	})
	if err != nil {
		return false, err
	}
	return r.Continue, nil
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

func (c *EngineController) SetModel(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("empty model name")
	}
	if c.proj == nil {
		return errors.New("project not available")
	}
	if err := c.proj.LoadConfig(); err != nil {
		return err
	}
	cfg, ok := c.proj.Config().FindModel(name)
	if !ok {
		// Not a configured model: keep the primary's connection settings and
		// only swap the name (arbitrary-model workflow).
		cfg = c.proj.Config().Model()
		cfg.Name = name
	}
	c.Cancel()
	c.initGate(c.proj.Config().Permissions)
	if c.engine == nil {
		return errors.New("agent not configured")
	}
	c.engine.SetPermission(c.gate, c.askPermission)
	c.engine.SetContinueAsk(c.askContinue)
	c.engine.SetJobs(c.engineJobs())
	if _, _, err := c.ReloadExtensions(); err != nil {
		debuglog.Logf("extension: reload on SetModel: %v", err)
	}
	c.engine.SetModel(cfg)
	c.modelCfg = cfg
	return nil
}

func (c *EngineController) ImageEnabled() bool {
	return c.modelCfg.ImageEnabled
}

func (c *EngineController) SessionID() string {
	if c.engine == nil {
		return ""
	}
	return c.engine.SessionID()
}

func (c *EngineController) SessionDir() string {
	return c.sessionDir
}

func (c *EngineController) LiveJobCount() int {
	if c.jobs == nil {
		return 0
	}
	return c.jobs.LiveCount()
}

func (c *EngineController) SessionFile() string {
	if c.engine == nil {
		return ""
	}
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
	eng, err := c.openEngine(cfg, extRunner, id)
	if err != nil {
		return "", err
	}
	if sessCwd := eng.SessionCwd(); sessCwd != "" && c.cwd != "" && sessCwd != c.cwd {
		cwdWarning = fmt.Sprintf("cwd %s ≠ %s", filepath.Base(sessCwd), filepath.Base(c.cwd))
	}
	c.engine = eng
	c.modelCfg = cfg
	c.emitSessionStart("resume", eng.SessionID(), prevID)
	return cwdWarning, nil
}

// Clear starts a brand-new persisted session. Caller must ensure no agent
// stream / local bash is in flight.
func (c *EngineController) Clear() error {
	prevID, err := c.beginSessionSwitch("new", "", false)
	if err != nil {
		return err
	}
	cfg, err := c.resolveModel()
	if err != nil {
		return err
	}
	engine, err := c.openEngine(cfg, c.Extensions(), "")
	if err != nil {
		return err
	}
	c.engine = engine
	c.modelCfg = cfg
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
	if c.modelCfg.Name != "" {
		return c.modelCfg, nil
	}
	if c.proj == nil {
		return llm.ModelConfig{}, errors.New("project not available")
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
) (*agent.Engine, error) {
	opts := []agent.SessionOption{
		agent.WithCwd(c.cwd),
		agent.WithSessionDir(c.sessionDir),
		agent.WithPersist(true),
	}
	if resumeID != "" {
		opts = append(opts, agent.WithResumeID(resumeID))
	}
	sess, err := agent.NewSession(opts...)
	if err != nil {
		return nil, err
	}
	return agent.NewEngine(cfg, sess,
		agent.WithGate(c.gate),
		agent.WithAsk(c.askPermission),
		agent.WithContinueAsk(c.askContinue),
		agent.WithJobs(c.engineJobs()),
		agent.WithExtensions(extRunner),
		agent.WithMCP(c.mcpPool),
	)
}

func (c *EngineController) ReplaySnapshot() session.Snapshot {
	if c.engine == nil || c.engine.Session() == nil {
		return session.Snapshot{}
	}
	return session.ReplaySnapshot(c.engine.Session().PathEntries(), c.engine.ToolDetail)
}

func (c *EngineController) StartPrompt(text string, pendingSkills []string, images []llm.Image) {
	ctx, cancel := context.WithCancel(context.Background())
	c.streamMu.Lock()
	if c.streamCancel != nil {
		c.streamCancel()
	}
	c.streamCancel = cancel
	c.streamGen++
	gen := c.streamGen
	c.streamMu.Unlock()

	go c.runLoop(ctx, gen, text, pendingSkills, images)
}

func (c *EngineController) Cancel() {
	c.streamMu.Lock()
	cancel := c.streamCancel
	c.streamMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *EngineController) Close() {
	c.sessionShutdown("quit", c.SessionID())
	c.Cancel()
	if c.unsubJobs != nil {
		c.unsubJobs()
		c.unsubJobs = nil
	}
	if c.jobs != nil {
		_ = c.jobs.Close(context.Background())
	}
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

func (c *EngineController) alive(gen int) bool {
	c.streamMu.Lock()
	ok := c.streamGen == gen
	c.streamMu.Unlock()
	return ok
}

func (c *EngineController) waitOrDone(ctx context.Context, gen int, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	}
	return c.alive(gen)
}

func (c *EngineController) publish(m Msg) {
	if c.bus != nil {
		c.bus.Publish(m)
	}
}

func (c *EngineController) publishLoopError(gen int, errText string) {
	if !c.alive(gen) {
		return
	}
	c.publish(SessionEventMsg{Event: session.AssistantMessageUpdate{Message: session.Message{
		ID:    fmt.Sprintf("assistant-error-%d", time.Now().UnixNano()),
		State: session.StateError,
		Text:  errText,
		Content: []session.ContentBlock{
			{Type: session.BlockText, Text: errText},
		},
	}}})
}

func (c *EngineController) runLoop(
	ctx context.Context,
	gen int,
	prompt string,
	pendingSkills []string,
	images []llm.Image,
) {
	if !c.waitOrDone(ctx, gen, 120*time.Millisecond) {
		return
	}
	c.publish(FooterMsg{Kind: FooterSetActivity, Activity: ActivityStreaming})

	if c.engine == nil {
		c.publishLoopError(gen, "agent not configured")
		return
	}

	for ev, err := range c.engine.Loop(ctx, prompt, agent.LoopOpts{
		PendingSkills: pendingSkills,
		Images:        images,
	}) {
		if !c.alive(gen) {
			return
		}
		if err != nil {
			c.publishLoopError(gen, err.Error())
			return
		}
		if ev != nil {
			c.publish(SessionEventMsg{Event: ev})
		}
	}
}
