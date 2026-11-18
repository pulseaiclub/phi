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

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/debuglog"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/project/model"
	"github.com/pulseaiclub/phi/internal/session"
)

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

	agentsEnabled syncBool
	mcpPool       *mcp.Pool

	lastJobProgress sync.Map // progressSlot → progressSig
}

type syncBool struct {
	v atomic.Bool
}

func (s *syncBool) Load() bool   { return s.v.Load() }
func (s *syncBool) Store(v bool) { s.v.Store(v) }

// NewController returns a live EngineController. proj must be non-nil.
// Failure returns (nil, err), never a half-initialized value.
func NewController(bus *Bus, proj *project.Project) (*EngineController, error) {
	if err := proj.LoadConfig(); err != nil {
		return nil, err
	}
	config := proj.Config()

	c := &EngineController{
		bus:        bus,
		proj:       proj,
		cwd:        proj.Root(),
		sessionDir: proj.SessionDir(),
		modelCfg:   config.Model(),
		roleModels: config.Agents.Models.RoleOverrides(),
	}
	c.agentsEnabled.Store(config.Agents.Enabled)

	jobs, err := agent.NewJobManager(proj.JobsDir(), c.modelCfg, c.modelForRole)
	if err != nil {
		return nil, err
	}
	c.jobs = jobs

	if pool, err := mcp.LoadPool(proj.MCPConfigFile()); err != nil {
		debuglog.Logf("mcp: load: %v", err)
	} else {
		c.mcpPool = pool
	}

	eng, _, err := c.openEngine(c.modelCfg, "")
	if err != nil {
		return nil, err
	}
	c.engine = eng
	c.startJobProgress()
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
	cfg := c.resolveModelName(name)
	c.Cancel()
	c.engine.SetJobs(c.engineJobs())
	c.modelMu.Lock()
	defer c.modelMu.Unlock()
	if err := c.engine.Session().SetModel(name); err != nil {
		return err
	}
	c.engine.SetModelWithHooks(cfg, model.HooksForConfig(cfg))
	c.modelCfg = cfg
	return nil
}

func (c *EngineController) resolveModelName(name string) llm.ModelConfig {
	if m, ok := c.proj.Config().FindModel(name); ok {
		return m
	}
	m := c.proj.Config().Model()
	m.Name = name
	return m
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

	c.commitSessionSwitch("resume", prevID, id)
	eng, cfg, err := c.openEngine(cfg, id)
	if err != nil {
		return "", err
	}
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
	c.commitSessionSwitch("new", prevID, "")
	engine, cfg, err := c.openEngine(cfg, "")
	if err != nil {
		return err
	}
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
	eng, err := c.newEngine(cfg, forked)
	if err != nil {
		c.discardFork(forked.ID())
		return "", err
	}
	c.commitSessionSwitch("fork", prevID, forked.ID())
	c.activateSession(eng, cfg, "fork", prevID)
	return forked.ID(), nil
}

// activateSession publishes the new session only after installing its engine and model together.
func (c *EngineController) activateSession(eng *agent.Engine, cfg llm.ModelConfig, _, _ string) {
	c.modelMu.Lock()
	c.engine = eng
	c.modelCfg = cfg
	c.modelMu.Unlock()
}

// discardFork drops a fork the controller never switched to, so a failed switch
// does not leave an unreachable session in the session list.
func (c *EngineController) discardFork(id string) {
	if _, err := session.DeleteSession(c.sessionDir, id); err != nil {
		debuglog.Logf("session: drop unused fork %s: %v", id, err)
	}
}

// preflightSessionSwitch validates that a session switch may happen.
func (c *EngineController) preflightSessionSwitch(_, _ string) (prevID string, err error) {
	if c.sessionDir == "" {
		return "", errors.New("session directory not configured")
	}
	prevID = c.SessionID()
	return prevID, nil
}

// commitSessionSwitch retires the outgoing session: the in-flight stream is
// cancelled before the new engine is installed.
func (c *EngineController) commitSessionSwitch(_, _, _ string) {
	c.Cancel()
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
		cfg = c.resolveModelName(name)
	}
	eng, err := c.newEngine(cfg, sess)
	if err != nil {
		return nil, llm.ModelConfig{}, err
	}
	return eng, cfg, nil
}

// newEngine binds a session to a fresh engine on the controller-wide gate,
// jobs, MCP pool and hooks. Every session switch funnels through
// here, so a forked session runs on exactly the wiring of a resumed one.
func (c *EngineController) newEngine(
	cfg llm.ModelConfig,
	sess *agent.Session,
) (*agent.Engine, error) {
	return agent.NewEngine(cfg, sess,
		agent.WithJobs(c.engineJobs()),
		agent.WithMCP(c.mcpPool),
		agent.WithHooks(model.HooksForConfig(cfg)),
	)
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
	c.Cancel()
	c.unsubJobs()
	_ = c.jobs.Close(context.Background())
	if c.mcpPool != nil {
		_ = c.mcpPool.Close()
		c.mcpPool = nil
	}
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
	// Publish completion only after the iterator's cleanup has finished,
	// so Retry cannot start while the failed turn is unwinding.
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
