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
	modelCfg   llm.ModelConfig
	// roleModels maps sub-agent role → model name. Empty → inherit modelCfg.
	roleModels map[job.Role]string
	jobs       *job.Manager
	unsubJobs  func()

	agentsEnabled atomic.Bool
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
		bus:        bus,
		proj:       proj,
		cwd:        cwd,
		sessionDir: sessionDir,
		modelCfg:   modelCfg,
		roleModels: roleModels,
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
		roleModelConfigs(modelCfg, roleModels, config),
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

	eng, err := buildEngine(modelCfg, sess, jobs, config.Agents.Enabled, mcpPool)
	if err != nil {
		return nil, err
	}
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
	sess, modelCfg := c.sess, c.modelSnapshot()
	cfg := c.engineConfig(modelCfg)
	cfg.Jobs = nil
	if v {
		cfg.Jobs = c.jobs
	}
	eng, err := cfg.NewEngine(modelCfg, sess)
	if err != nil {
		return err
	}
	c.agentsEnabled.Store(v)
	c.installEngine(eng, sess, modelCfg)
	return nil
}

// AgentsEnabled reports whether sub-agent (agent_*) tools are registered.
func (c *EngineController) AgentsEnabled() bool { return c.agentsEnabled.Load() }

func roleModelConfigs(
	parent llm.ModelConfig,
	roleModels map[job.Role]string,
	config *project.Config,
) map[job.Role]llm.ModelConfig {
	models := map[job.Role]llm.ModelConfig{
		job.RoleExplore: parent,
		job.RoleReview:  parent,
		job.RoleWorker:  parent,
	}
	for role, name := range roleModels {
		if m, ok := config.FindModel(name); ok {
			models[role] = m
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
			return fmt.Errorf("unknown model %q", name)
		}
	}
	c.modelMu.Lock()
	defer c.modelMu.Unlock()
	if name == "" {
		delete(c.roleModels, r)
	} else {
		c.roleModels[r] = name
	}
	c.jobs.SetRoleModels(roleModelConfigs(c.modelCfg, c.roleModels, c.proj.Config()))
	return nil
}

func (c *EngineController) engineJobs() *job.Manager {
	if !c.agentsEnabled.Load() {
		return nil
	}
	return c.jobs
}

func buildEngine(
	cfg llm.ModelConfig,
	sess *agent.Session,
	jobs *job.Manager,
	agentsEnabled bool,
	mcpPool *mcp.Pool,
) (*agent.Engine, error) {
	if !agentsEnabled {
		jobs = nil
	}
	return (agent.EngineConfig{
		Jobs: jobs, MCP: mcpPool, Hooks: model.HooksForConfig(cfg),
	}).NewEngine(cfg, sess)
}

// resolveModelConfig keeps arbitrary model names on the primary connection.
func resolveModelConfig(cfg *project.Config, name string) llm.ModelConfig {
	if m, ok := cfg.FindModel(name); ok {
		return m
	}
	m := cfg.Model()
	m.Name = name
	return m
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
	cfg := resolveModelConfig(c.proj.Config(), name)
	eng, err := c.newEngine(cfg, sess)
	if err == nil {
		c.Cancel()
		err = sess.SetModel(name)
	}
	if err != nil {
		return err
	}
	c.installEngine(eng, sess, cfg)
	return nil
}

// SetThinkLevel changes the thinking level for the current session.
func (c *EngineController) SetThinkLevel(mode llm.ThinkMode) error {
	cfg := c.modelSnapshot()
	cfg.Think = llm.ThinkConfig{Mode: mode, Enabled: mode != llm.Off}
	if err := model.ValidateThinking(cfg); err != nil {
		return err
	}
	eng, err := c.newEngine(cfg, c.sess)
	if err != nil {
		return err
	}
	c.installEngine(eng, c.sess, cfg)
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
	if err := c.preflightSessionSwitch(); err != nil {
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
	if name := sess.ModelName(); name != "" {
		cfg = resolveModelConfig(c.proj.Config(), name)
	}
	eng, err := c.newEngine(cfg, sess)
	if err != nil {
		return "", err
	}
	c.commitSessionSwitch()
	if sessCwd := sess.Cwd(); sessCwd != "" && c.cwd != "" && sessCwd != c.cwd {
		cwdWarning = fmt.Sprintf("cwd %s ≠ %s", filepath.Base(sessCwd), filepath.Base(c.cwd))
	}
	c.activateSession(eng, sess, cfg)
	return cwdWarning, nil
}

// NewSession starts a brand-new persisted session. Caller must ensure no agent
// stream / local bash is in flight.
func (c *EngineController) NewSession() error {
	if err := c.preflightSessionSwitch(); err != nil {
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
	engine, err := c.newEngine(cfg, sess)
	if err != nil {
		return err
	}
	c.commitSessionSwitch()
	c.activateSession(engine, sess, cfg)
	return nil
}

// ForkSession copies the active session's current branch into a new persisted
// session and switches the controller to it. The fork file exists and is bound
// to an engine before the live session is touched: any failure leaves the
// source session active and its history intact.
func (c *EngineController) ForkSession() (string, error) {
	if err := c.preflightSessionSwitch(); err != nil {
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
	eng, err := c.newEngine(cfg, forked)
	if err != nil {
		c.discardFork(forked.ID())
		return "", err
	}
	c.commitSessionSwitch()
	c.activateSession(eng, forked, cfg)
	return forked.ID(), nil
}

// activateSession installs the new session only after its engine, session, and model are ready together.
func (c *EngineController) activateSession(eng *agent.Engine, sess *agent.Session, cfg llm.ModelConfig) {
	c.installEngine(eng, sess, cfg)
}

// installEngine swaps the live engine and the session it is bound to as one unit.
func (c *EngineController) installEngine(eng *agent.Engine, sess *agent.Session, cfg llm.ModelConfig) {
	c.modelMu.Lock()
	defer c.modelMu.Unlock()
	c.engine = eng
	c.sess = sess
	c.modelCfg = cfg
	c.jobs.SetRoleModels(roleModelConfigs(cfg, c.roleModels, c.proj.Config()))
}

// discardFork drops a fork the controller never switched to, so a failed switch
// does not leave an unreachable session in the session list.
func (c *EngineController) discardFork(id string) {
	if _, err := session.DeleteSession(c.sessionDir, id); err != nil {
		debuglog.Logf("session: drop unused fork %s: %v", id, err)
	}
}

func (c *EngineController) preflightSessionSwitch() error {
	if c.sessionDir == "" {
		return errors.New("session directory not configured")
	}
	return nil
}

// commitSessionSwitch retires the outgoing session's in-flight stream.
func (c *EngineController) commitSessionSwitch() {
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

// newEngine binds a session to a fresh engine on the controller-wide gate,
// jobs, MCP pool and hooks. Every session switch funnels through here, so a
// forked session runs on exactly the wiring of a resumed one.
func (c *EngineController) newEngine(cfg llm.ModelConfig, sess *agent.Session) (*agent.Engine, error) {
	return c.engineConfig(cfg).NewEngine(cfg, sess)
}

func (c *EngineController) engineConfig(cfg llm.ModelConfig) agent.EngineConfig {
	return agent.EngineConfig{Jobs: c.engineJobs(), MCP: c.mcpPool, Hooks: model.HooksForConfig(cfg)}
}

func (c *EngineController) ReplaySnapshot() session.Snapshot {
	return session.ReplaySnapshot(c.sess.PathEntries(), c.engine.ToolDetail)
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
	// Publish completion only after the iterator's cleanup
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
