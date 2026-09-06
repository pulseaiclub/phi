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

// EngineController owns agent.Engine lifecycle and stream cancellation.
// It talks to the UI only by publishing Msg values onto the Bus.
//
// Construction: NewController(bus, proj, cwd). Callers (cmd) assemble
// collaborators; EngineController does not call project.GetDefaultProject.
type EngineController struct {
	engine *agent.Engine
	proj   *project.Project

	streamMu     sync.Mutex
	streamCancel context.CancelFunc
	streamGen    int
	streamDone   chan struct{}
	treeCancel   context.CancelFunc
	treeBusy     atomic.Bool

	bus *Bus

	sessionDir string
	cwd        string
	modelCfg   llm.ModelConfig
	jobs       *job.Manager
	unsubJobs  func()

	gate          permission.Gate
	askTimeoutSec int
	allowAll      atomic.Bool // session-wide allow-all for this process
	agentsEnabled atomic.Bool // when false, agent_* tools are not registered
	extRunner     atomic.Pointer[extension.Runner]
	mcpPool       *mcp.Pool

	// lastJobProgress dedupes identical Progress publishes (key → signature).
	lastJobProgress sync.Map
}

// NewController wires bus + project into a ready EngineController with a live Engine.
// proj must be non-nil (typically already LoadConfig'd by cmd). On failure it
// returns (nil, err) — never a half-initialized EngineController.
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

	c := &EngineController{
		bus:           bus,
		proj:          proj,
		cwd:           cwd,
		sessionDir:    proj.SessionDir(),
		askTimeoutSec: 120,
		modelCfg:      proj.Config().Model(),
	}
	// Default: no permission prompts. Toggle via command palette → settings → permissions.
	c.allowAll.Store(true)

	config := proj.Config()

	c.initGate(config.Permissions)
	c.agentsEnabled.Store(config.Agents.Enabled)

	extRunner := loadExtensions(proj)
	c.extRunner.Store(extRunner)
	c.bindExtensionHost(extRunner)

	jobs, err := agent.NewJobManager(proj.JobsDir(), c.modelCfg, func() llm.ModelConfig {
		return c.modelCfg
	}, c.Extensions)
	if err != nil {
		return nil, err
	}
	c.jobs = jobs

	if pool, err := mcp.LoadPool(proj.MCPConfigFile()); err != nil {
		debuglog.Logf("mcp: load: %v", err)
	} else {
		c.mcpPool = pool
	}

	sess, err := agent.NewSession(
		agent.WithCwd(cwd),
		agent.WithSessionDir(c.sessionDir),
		agent.WithPersist(true),
	)
	if err != nil {
		return nil, err
	}
	eng, err := agent.NewEngine(c.modelCfg, sess,
		agent.WithGate(c.gate),
		agent.WithAsk(c.askPermission),
		agent.WithContinueAsk(c.askContinue),
		agent.WithJobs(c.engineJobs()),
		agent.WithExtensions(extRunner),
		agent.WithMCP(c.mcpPool),
	)
	if err != nil {
		return nil, err
	}
	c.engine = eng
	c.startJobProgress()
	c.emitSessionStart("startup", eng.SessionID(), "")
	return c, nil
}

func (c *EngineController) startJobProgress() {
	if c.jobs == nil || c.bus == nil {
		return
	}
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

// shouldPublishJobProgress drops duplicate progress for the same child tool
// slot (same status/detail/name). Status transitions and new children still publish.
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
	inner, err := permission.NewGate(policy, permission.WorkspaceRoot())
	if err != nil {
		inner, err = permission.NewGate(permission.DefaultPolicy(), permission.WorkspaceRoot())
		if err != nil {
			c.gate = &permission.BypassGate{Inner: permission.AllowAll{}, Enabled: &c.allowAll}
			return
		}
	}
	c.gate = &permission.BypassGate{Inner: inner, Enabled: &c.allowAll}
}

// AllowAll reports whether permission prompts are bypassed for this session.
func (c *EngineController) AllowAll() bool {
	if c == nil {
		return true
	}
	return c.allowAll.Load()
}

// SetAllowAll enables or disables session-wide permission bypass.
func (c *EngineController) SetAllowAll(v bool) {
	if c == nil {
		return
	}
	c.allowAll.Store(v)
}

// AgentsEnabled reports whether sub-agent tools are registered on the main engine.
func (c *EngineController) AgentsEnabled() bool {
	if c == nil {
		return false
	}
	return c.agentsEnabled.Load()
}

// SetAgentsEnabled registers or removes agent_* tools for this session.
func (c *EngineController) SetAgentsEnabled(v bool) {
	if c == nil {
		return
	}
	c.agentsEnabled.Store(v)
	if c.engine != nil {
		c.engine.SetJobs(c.engineJobs())
	}
}

// engineJobs returns the job manager only when sub-agents are enabled.
func (c *EngineController) engineJobs() *job.Manager {
	if c == nil || !c.agentsEnabled.Load() {
		return nil
	}
	return c.jobs
}

// Extensions returns the currently loaded extension runner (may be nil).
func (c *EngineController) Extensions() *extension.Runner {
	if c == nil {
		return nil
	}
	return c.extRunner.Load()
}

// ReloadExtensions re-discovers extensions from disk and swaps the runner on the
// engine (and on future sub-agents via Extensions()).
func (c *EngineController) ReloadExtensions() (loaded int, warns []extension.Warning, err error) {
	if c == nil {
		return 0, nil, errors.New("controller not initialized")
	}
	proj := c.proj
	if proj == nil {
		return 0, nil, errors.New("project not available")
	}
	r, warns, err := extension.Load(proj.Global().ExtensionsDir(), proj.ExtensionsDir())
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

// swapExtensionRunner replaces the live runner, closing the previous one and
// rebinding host UI callbacks onto the replacement.
func (c *EngineController) swapExtensionRunner(r *extension.Runner) {
	if c == nil {
		return
	}
	if prev := c.extRunner.Swap(r); prev != nil {
		prev.Close()
	}
	c.bindExtensionHost(r)
}

// ListExtensions returns the current on-disk discovery (does not swap the runner).
func (c *EngineController) ListExtensions() ([]extension.Discovered, []extension.Warning, error) {
	if c == nil {
		return nil, nil, errors.New("controller not initialized")
	}
	proj := c.proj
	if proj == nil {
		return nil, nil, errors.New("project not available")
	}
	return extension.Discover(proj.Global().ExtensionsDir(), proj.ExtensionsDir())
}

// loadExtensions discovers ~/.phi/extensions and <cwd>/.phi/extensions.
// Load errors are non-fatal (fail-open: no extensions). Child engines stay nil until spawn.
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
	if n := len(warns); n > 0 {
		debuglog.Logf("extension: %d warning(s) while loading", n)
	}
}

func (c *EngineController) bindExtensionHost(r *extension.Runner) {
	if c == nil || r == nil {
		return
	}
	cwd := ""
	sessionID := ""
	if c.engine != nil {
		cwd = c.engine.SessionCwd()
		sessionID = c.engine.SessionID()
	} else if c.proj != nil {
		cwd = c.proj.Root()
	}
	ui := extension.BusUI{
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
		ConfirmFn: func(req ext.ConfirmRequest) ext.ConfirmReply {
			return c.askExtConfirm(req)
		},
	}
	r.Bind(ext.HostOpts{
		UI:        ui,
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

// askPermission blocks until the confirmation UI answers.
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

	timeout := time.Duration(c.askTimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case r := <-reply:
		if r.AllowSession || r.AllowPersistent {
			c.allowAll.Store(true)
		}
		if r.AllowPersistent {
			if c.proj != nil {
				_ = project.SetDangerouslyAllowAll(c.proj.Global(), true)
			}
		}
		return permission.AskResult{Approved: r.Approved, Feedback: r.Feedback}, nil
	case <-ctx.Done():
		c.publish(OverlayMsg{Kind: OverlayPermissionDismiss})
		return permission.AskResult{}, ctx.Err()
	case <-timer.C:
		c.publish(OverlayMsg{Kind: OverlayPermissionDismiss})
		return permission.AskResult{}, nil
	}
}

// askContinue blocks until the user chooses to continue or stop after max rounds.
func (c *EngineController) askContinue(ctx context.Context, maxRounds int) (bool, error) {
	reply := make(chan ContinueReply, 1)
	c.publish(OverlayMsg{Kind: OverlayContinueAsk, MaxRounds: maxRounds, ContReply: reply})

	timeout := time.Duration(c.askTimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case r := <-reply:
		return r.Continue, nil
	case <-ctx.Done():
		c.publish(OverlayMsg{Kind: OverlayContinueDismiss})
		return false, ctx.Err()
	case <-timer.C:
		c.publish(OverlayMsg{Kind: OverlayContinueDismiss})
		return false, nil
	}
}

// askExtConfirm blocks until the user answers an extension Confirm dialog.
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
	timeout := time.Duration(c.askTimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-reply:
		return ext.ConfirmReply{OK: r.OK}
	case <-timer.C:
		c.publish(OverlayMsg{Kind: OverlayExtConfirmDismiss})
		return ext.ConfirmReply{}
	}
}

// SetModel replaces the LLM client while keeping the same session tree.
func (c *EngineController) SetModel(name string) error {
	if c.Running() || c.TreeBusy() {
		return errors.New("finish or cancel the current operation before switching models")
	}
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
	if err := c.engine.SetModel(cfg); err != nil {
		return err
	}
	c.modelCfg = cfg
	return c.RecordHistory(session.HistoryEntry{Kind: "model", Text: "Model: " + name})
}

// ImageEnabled reports whether the active model accepts attached images.
func (c *EngineController) ImageEnabled() bool {
	return c != nil && c.modelCfg.ImageEnabled
}

// SessionID returns the short-form-friendly session id.
func (c *EngineController) SessionID() string {
	if c.engine == nil {
		return ""
	}
	return c.engine.SessionID()
}

// SessionDir returns the directory where session JSONL files are stored.
func (c *EngineController) SessionDir() string {
	if c == nil {
		return ""
	}
	return c.sessionDir
}

// LiveJobCount returns in-flight sub-agent jobs (0 if jobs disabled).
func (c *EngineController) LiveJobCount() int {
	if c == nil || c.jobs == nil {
		return 0
	}
	return c.jobs.LiveCount()
}

// SessionFile returns the JSONL path when persisting.
func (c *EngineController) SessionFile() string {
	if c.engine == nil {
		return ""
	}
	return c.engine.SessionFile()
}

// Resume loads a prior session by id (exact or unique prefix).
// On success the engine session is replaced; caller should refresh the UI transcript.
// If the resumed session cwd differs from the process cwd, cwdWarning is non-empty.
func (c *EngineController) Resume(id string) (cwdWarning string, err error) {
	if c.Running() || c.TreeBusy() {
		return "", errors.New("finish or cancel the current operation before resuming a session")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("empty session id")
	}
	if c.sessionDir == "" {
		return "", errors.New("session directory not configured")
	}

	prevID := c.SessionID()
	out := c.sessionBeforeSwitch("resume", prevID, id)
	c.publishSessionEffects(out)
	if out.Denied {
		reason := out.Reason
		if reason == "" {
			reason = "session switch denied by extension"
		}
		return "", errors.New(reason)
	}

	c.Cancel()
	c.sessionShutdown("resume", prevID)

	cfg := c.modelCfg
	if cfg.Name == "" {
		if c.proj == nil {
			return "", errors.New("project not available")
		}
		if err := c.proj.LoadConfig(); err != nil {
			return "", err
		}
		cfg = c.proj.Config().Model()
	}

	extRunner := loadExtensions(c.proj)
	// Close the previous runner before attaching the new one so UI host
	// callbacks and subprocesses do not leak across /resume.
	c.swapExtensionRunner(extRunner)
	sess, err := agent.NewSession(
		agent.WithCwd(c.cwd),
		agent.WithSessionDir(c.sessionDir),
		agent.WithPersist(true),
		agent.WithResumeID(id),
	)
	if err != nil {
		return "", err
	}
	eng, err := agent.NewEngine(cfg, sess,
		agent.WithGate(c.gate),
		agent.WithAsk(c.askPermission),
		agent.WithContinueAsk(c.askContinue),
		agent.WithJobs(c.engineJobs()),
		agent.WithExtensions(extRunner),
		agent.WithMCP(c.mcpPool),
	)
	if err != nil {
		return "", err
	}
	if sessCwd := eng.SessionCwd(); sessCwd != "" && c.cwd != "" && sessCwd != c.cwd {
		// Keep toast-friendly: basenames only, no full paths.
		cwdWarning = fmt.Sprintf("cwd %s ≠ %s", filepath.Base(sessCwd), filepath.Base(c.cwd))
	}
	c.engine = eng
	c.modelCfg = cfg
	c.emitSessionStart("resume", eng.SessionID(), prevID)
	return cwdWarning, nil
}

// Clear starts a brand-new persisted session (empty transcript, new id).
// Caller must ensure no agent stream / local bash is in flight.
func (c *EngineController) Clear() error {
	if c.Running() || c.TreeBusy() {
		return errors.New("finish or cancel the current operation before clearing the session")
	}
	if c.sessionDir == "" {
		return errors.New("session directory not configured")
	}

	prevID := c.SessionID()
	out := c.sessionBeforeSwitch("new", prevID, "")
	c.publishSessionEffects(out)
	if out.Denied {
		reason := out.Reason
		if reason == "" {
			reason = "session switch denied by extension"
		}
		return errors.New(reason)
	}
	c.sessionShutdown("new", prevID)

	cfg := c.modelCfg
	if cfg.Name == "" {
		if c.proj == nil {
			return errors.New("project not available")
		}
		if err := c.proj.LoadConfig(); err != nil {
			return err
		}
		cfg = c.proj.Config().Model()
	}

	extRunner := c.Extensions()
	sess, err := agent.NewSession(
		agent.WithCwd(c.cwd),
		agent.WithSessionDir(c.sessionDir),
		agent.WithPersist(true),
	)
	if err != nil {
		return err
	}
	engine, err := agent.NewEngine(cfg, sess,
		agent.WithGate(c.gate),
		agent.WithAsk(c.askPermission),
		agent.WithContinueAsk(c.askContinue),
		agent.WithJobs(c.engineJobs()),
		agent.WithExtensions(extRunner),
		agent.WithMCP(c.mcpPool),
	)
	if err != nil {
		return err
	}
	c.engine = engine
	c.modelCfg = cfg
	c.emitSessionStart("new", engine.SessionID(), prevID)
	return nil
}

// ReplaySnapshot builds a UI transcript snapshot from the engine session,
// resolving tool-call details through the live tool registry so resumed tool
// rows match the original rendering.
func (c *EngineController) ReplaySnapshot() session.Snapshot {
	if c.engine == nil || c.engine.Session() == nil {
		return session.Snapshot{}
	}
	return session.ReplaySnapshot(c.engine.Session().PathEntries(), c.engine.ToolDetail)
}

// StartPrompt cancels any in-flight stream and starts a new agent loop.
func (c *EngineController) StartPrompt(text string, pendingSkills []string, images []llm.Image) {
	ctx, cancel := context.WithCancel(context.Background())
	c.streamMu.Lock()
	if c.treeBusy.Load() {
		c.streamMu.Unlock()
		cancel()
		c.publish(
			ToastMsg{
				Message:  "Tree navigation is in progress; retry after it finishes",
				Kind:     toast.ToastWarning,
				Duration: 3 * time.Second,
			},
		)
		return
	}
	if c.streamCancel != nil {
		c.streamCancel()
	}
	previous := c.streamDone
	done := make(chan struct{})
	c.streamDone = done
	c.streamCancel = cancel
	c.streamGen++
	gen := c.streamGen
	c.streamMu.Unlock()

	go func() {
		defer func() {
			cancel()
			c.streamMu.Lock()
			if c.streamDone == done {
				c.streamDone = nil
				c.streamCancel = nil
			}
			close(done)
			c.streamMu.Unlock()
		}()
		if previous != nil {
			<-previous
		}
		c.runLoop(ctx, gen, text, pendingSkills, images)
	}()
}

// Cancel aborts the current stream context (if any).
func (c *EngineController) Cancel() {
	c.streamMu.Lock()
	cancel := c.streamCancel
	c.streamMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Close cancels the stream and shuts down jobs, MCP, and extensions.
func (c *EngineController) Close() {
	c.CancelTreeNavigation()
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
	out := r.EmitSessionShutdown(ext.SessionShutdownEvent{
		Reason: reason,
	})
	c.publishSessionEffects(out)
}

func (c *EngineController) emitSessionStart(reason, sessionID, previousID string) {
	r := c.Extensions()
	if r == nil {
		return
	}
	r.SetMeta(sessionID, c.cwd)
	out := r.EmitSessionStart(ext.SessionStartEvent{
		Reason:            reason,
		PreviousSessionID: previousID,
	})
	c.publishSessionEffects(out)
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

// Alive reports whether the stream generation still matches gen.
func (c *EngineController) Alive(gen int) bool {
	c.streamMu.Lock()
	ok := c.streamGen == gen
	c.streamMu.Unlock()
	return ok
}

func (c *EngineController) waitOrDone(ctx context.Context, gen int, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
	}
	return c.Alive(gen)
}

func (c *EngineController) publish(m Msg) {
	if c.bus != nil {
		c.bus.Publish(m)
	}
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
	c.publish(FooterMsg{Kind: FooterSetActivity, Activity: ActivityStreaming, Gen: gen})

	if c.engine == nil {
		errText := "agent not configured"
		if !c.Alive(gen) {
			return
		}
		c.publish(SessionEventMsg{Gen: gen, Event: session.AssistantMessageUpdate{Message: session.Message{
			ID:    fmt.Sprintf("assistant-error-%d", time.Now().UnixNano()),
			State: session.StateError,
			Text:  errText,
			Content: []session.ContentBlock{
				{Type: session.BlockText, Text: errText},
			},
		}}})
		return
	}

	for ev, err := range c.engine.Loop(ctx, prompt, agent.LoopOpts{
		PendingSkills: pendingSkills,
		Images:        images,
	}) {
		if !c.Alive(gen) {
			return
		}
		if err != nil {
			errText := err.Error()
			c.publish(SessionEventMsg{Gen: gen, Event: session.AssistantMessageUpdate{Message: session.Message{
				ID:    fmt.Sprintf("assistant-error-%d", time.Now().UnixNano()),
				State: session.StateError,
				Text:  errText,
				Content: []session.ContentBlock{
					{Type: session.BlockText, Text: errText},
				},
			}}})
			return
		}
		if ev != nil {
			c.publish(SessionEventMsg{Gen: gen, Event: ev})
		}
	}
}
