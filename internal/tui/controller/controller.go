package controller

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/agent/prompt"
	"github.com/pulseaiclub/phi/internal/debuglog"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/project/model"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/session/compaction"
	"github.com/pulseaiclub/phi/internal/tools"
	"github.com/pulseaiclub/phi/internal/tools/agenttool"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

// EngineController owns the active session and its agent.Engine lifecycle.
// It talks to the UI only by publishing Msg values onto the Bus.
type EngineController struct {
	engine  *agent.Engine
	session *agent.Session
	proj    *project.Project

	// Background jobs and bash completions read the active model and session.
	// Publish them together with the engine on every session switch.
	sessionMu sync.RWMutex

	streamMu       sync.Mutex
	streamCancel   context.CancelFunc
	streamGen      int
	lastPrompt     LastPrompt
	promptAppended bool
	retryReady     bool

	bus *Bus

	sessionDir string
	cwd        string
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

	eng, sess, _, err := c.openEngine(c.modelCfg, "")
	if err != nil {
		return nil, err
	}
	c.engine = eng
	c.session = sess
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

func (c *EngineController) SetAgentsEnabled(v bool) error {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	eng, err := c.newEngine(c.modelCfg, c.session, v)
	if err != nil {
		return err
	}
	c.engine = eng
	c.agentsEnabled.Store(v)
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
	cfg := resolveModelConfig(c.proj.Config(), name)
	c.Cancel()
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	eng, err := c.newEngine(cfg, c.session, c.agentsEnabled.Load())
	if err != nil {
		return err
	}
	if err := c.session.SetModel(name); err != nil {
		return err
	}
	c.engine = eng
	c.modelCfg = cfg
	return nil
}

// SetThinkLevel changes the thinking level for the current session.
func (c *EngineController) SetThinkLevel(mode llm.ThinkMode) error {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	cfg := c.modelCfg
	cfg.Think = llm.ThinkConfig{Mode: mode, Enabled: mode != llm.Off}
	if err := model.ValidateThinking(cfg); err != nil {
		return err
	}
	eng, err := c.newEngine(cfg, c.session, c.agentsEnabled.Load())
	if err != nil {
		return err
	}
	c.engine = eng
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
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()
	return c.modelCfg
}

func (c *EngineController) ImageEnabled() bool {
	return c.modelSnapshot().ImageEnabled
}

func (c *EngineController) SessionID() string {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()
	return c.session.ID()
}

// AppendUserBash records a user "!!cmd" run in the current session so the next
// turn includes its output. sessionID guards a session switch that happened
// while the command was running: the output belongs to the old session, not
// whichever one is open now.
func (c *EngineController) AppendUserBash(sessionID, command, output string, exitCode int, cancelled bool) error {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()
	if sessionID != "" && c.session.ID() != sessionID {
		return nil
	}
	return c.session.AppendBash(command, output, exitCode, cancelled)
}

func (c *EngineController) SessionDir() string {
	return c.sessionDir
}

func (c *EngineController) SessionFile() string {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()
	return c.session.File()
}

func (c *EngineController) Resume(id string) (cwdWarning string, err error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("empty session id")
	}
	if err := c.checkSessionDir(); err != nil {
		return "", err
	}
	cfg, err := c.resolveModel()
	if err != nil {
		return "", err
	}

	c.Cancel()
	eng, sess, cfg, err := c.openEngine(cfg, id)
	if err != nil {
		return "", err
	}
	if sessCwd := sess.Cwd(); sessCwd != "" && c.cwd != "" && sessCwd != c.cwd {
		cwdWarning = fmt.Sprintf("cwd %s ≠ %s", filepath.Base(sessCwd), filepath.Base(c.cwd))
	}
	c.activateSession(eng, sess, cfg)
	return cwdWarning, nil
}

// NewSession starts a brand-new persisted session. Caller must ensure no agent
// stream / local bash is in flight.
func (c *EngineController) NewSession() error {
	if err := c.checkSessionDir(); err != nil {
		return err
	}
	cfg, err := c.resolveModel()
	if err != nil {
		return err
	}
	c.Cancel()
	engine, sess, cfg, err := c.openEngine(cfg, "")
	if err != nil {
		return err
	}
	c.activateSession(engine, sess, cfg)
	return nil
}

// ForkSession copies the active session's current branch into a new persisted
// session. The source stays active until the fork's engine is ready.
func (c *EngineController) ForkSession() (string, error) {
	if err := c.checkSessionDir(); err != nil {
		return "", err
	}
	c.sessionMu.RLock()
	forked, err := c.session.Fork()
	c.sessionMu.RUnlock()
	if err != nil {
		return "", err
	}
	cfg, err := c.resolveModel()
	if err != nil {
		c.discardFork(forked.ID())
		return "", err
	}
	eng, err := c.newEngine(cfg, forked, c.agentsEnabled.Load())
	if err != nil {
		c.discardFork(forked.ID())
		return "", err
	}
	c.Cancel()
	c.activateSession(eng, forked, cfg)
	return forked.ID(), nil
}

func (c *EngineController) activateSession(eng *agent.Engine, sess *agent.Session, cfg llm.ModelConfig) {
	c.sessionMu.Lock()
	c.engine = eng
	c.session = sess
	c.modelCfg = cfg
	c.sessionMu.Unlock()
}

// discardFork drops a fork the controller never switched to, so a failed switch
// does not leave an unreachable session in the session list.
func (c *EngineController) discardFork(id string) {
	if _, err := session.DeleteSession(c.sessionDir, id); err != nil {
		debuglog.Logf("session: drop unused fork %s: %v", id, err)
	}
}

func (c *EngineController) checkSessionDir() error {
	if c.sessionDir == "" {
		return errors.New("session directory not configured")
	}
	return nil
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

func resolveModelConfig(cfg *project.Config, name string) llm.ModelConfig {
	if m, ok := cfg.FindModel(name); ok {
		return m
	}
	m := cfg.Model()
	m.Name = name
	return m
}

func (c *EngineController) openEngine(
	cfg llm.ModelConfig,
	resumeID string,
) (*agent.Engine, *agent.Session, llm.ModelConfig, error) {
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
		return nil, nil, llm.ModelConfig{}, err
	}
	if name := sess.ModelName(); resumeID != "" && name != "" {
		cfg = resolveModelConfig(c.proj.Config(), name)
	}
	eng, err := c.newEngine(cfg, sess, c.agentsEnabled.Load())
	if err != nil {
		return nil, nil, llm.ModelConfig{}, err
	}
	return eng, sess, cfg, nil
}

func (c *EngineController) newEngine(cfg llm.ModelConfig, sess *agent.Session, enableJobs bool) (*agent.Engine, error) {
	var jobs *agenttool.Binding
	if enableJobs {
		jobs = agenttool.Bind(c.jobs, sess.ID(), sess.Cwd())
	}
	return NewParentEngine(cfg, sess, tools.DefaultTools(c.mcpPool), c.mcpPool, jobs)
}

// NewParentEngine uses the same tools for the model schema and for execution
// in both the TUI and headless runs. MCP contributes server names, not schemas.
func NewParentEngine(
	cfg llm.ModelConfig,
	sess *agent.Session,
	base []tooldef.Tool,
	pool *mcp.Pool,
	jobs *agenttool.Binding,
) (*agent.Engine, error) {
	list := base
	if jobs != nil {
		list = slices.Concat(base, jobs.Tools())
	}
	cwd := sess.Cwd()
	system := prompt.Build(cwd, cfg.SkillPaths, jobs != nil, jobs.MaxConcurrent(), pool.ServerNames())
	client := llmclient.NewClient(cfg, model.HooksForConfig(cfg), tooldef.Definitions(list), system)
	executor := agent.NewExecutor(tooldef.NewRegistry(list), cwd)
	return agent.NewEngine(compaction.LimitsForModel(cfg), sess, client, executor)
}

func (c *EngineController) ReplaySnapshot() session.Snapshot {
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()
	return session.ReplaySnapshot(c.session.PathEntries(), c.engine.ToolDetail)
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
	c.sessionMu.RLock()
	engine, skillPaths := c.engine, c.modelCfg.SkillPaths
	c.sessionMu.RUnlock()
	go c.runLoop(ctx, c.streamGen, engine, c.lastPrompt, skillPaths, c.promptAppended)
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

func (c *EngineController) runLoop(
	ctx context.Context,
	gen int,
	engine *agent.Engine,
	submission LastPrompt,
	skillPaths []string,
	resume bool,
) {
	if !c.waitOrDone(ctx, gen, 120*time.Millisecond) {
		return
	}
	var loopErr error
	// Retry must not start while the failed turn's iterator is still unwinding.
	defer func() { c.finishLoop(ctx, gen, loopErr) }()
	text := submission.Text
	if !resume {
		text = prompt.WithSkills(text, skillPaths, submission.Skills)
	}
	for ev, err := range engine.Loop(ctx, text, agent.LoopOpts{
		Images: submission.Images,
		Resume: resume,
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
