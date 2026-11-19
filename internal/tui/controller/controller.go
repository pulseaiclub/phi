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

	// Publish the active session, engine, and role models together.
	sessionMu sync.RWMutex

	streamMu       sync.Mutex
	streamCancel   context.CancelFunc
	streamGen      int
	lastPrompt     LastPrompt
	promptAppended bool
	retryReady     bool

	bus *Bus

	modelCfg llm.ModelConfig
	// roleModels maps sub-agent role → model name. Empty → inherit modelCfg.
	roleModels map[job.Role]string
	jobs       *job.Manager

	agentsEnabled atomic.Bool
	mcpPool       *mcp.Pool

	closeOnce sync.Once
}

// NewController returns a live EngineController with the supplied runtime dependencies.
// All dependencies are required. The engine and jobs must be configured for sess and proj's loaded config.
func NewController(
	bus *Bus,
	proj *project.Project,
	sess *agent.Session,
	jobs *job.Manager,
	mcpPool *mcp.Pool,
	engine *agent.Engine,
) *EngineController {
	config := proj.Config()
	c := &EngineController{
		bus:        bus,
		proj:       proj,
		modelCfg:   config.Model(),
		roleModels: config.Agents.Models.RoleOverrides(),
		jobs:       jobs,
		mcpPool:    mcpPool,
		engine:     engine,
		session:    sess,
	}
	c.agentsEnabled.Store(config.Agents.Enabled)
	c.startJobProgress()
	return c
}

func (c *EngineController) startJobProgress() {
	// Manager.Close owns the subscription's lifetime.
	ch, _ := c.jobs.Subscribe()
	go func() {
		last := make(map[progressSlot]progressSig)
		for p := range ch {
			if shouldPublishJobProgress(last, p) {
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

func shouldPublishJobProgress(last map[progressSlot]progressSig, p job.Progress) bool {
	slot := progressSlot{job: p.JobID, tool: p.ToolUseID}
	if p.ToolUseID == "" {
		slot.name = p.Name
		slot.detail = p.Detail
	}
	sig := progressSig{status: p.Status, name: p.Name, detail: p.Detail}
	if prev, ok := last[slot]; ok && prev == sig {
		return false
	}
	last[slot] = sig
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

func (c *EngineController) publishRoleModelsLocked() {
	c.jobs.SetRoleModels(c.proj.Config().ResolveRoleModels(c.modelCfg, c.roleModels))
}

// RoleModel returns the runtime model override for a sub-agent role.
// Empty means the role inherits the parent model.
func (c *EngineController) RoleModel(role string) string {
	r, err := job.ParseRole(role)
	if err != nil {
		return ""
	}
	c.sessionMu.RLock()
	defer c.sessionMu.RUnlock()
	return c.roleModels[r]
}

// SetRoleModel sets a runtime override that survives session switches.
// Empty name clears the override (inherit parent). Name must exist in config.
func (c *EngineController) SetRoleModel(role, name string) error {
	r, err := job.ParseRole(role)
	if err != nil {
		return err
	}
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	name = strings.TrimSpace(name)
	if name == "" {
		delete(c.roleModels, r)
	} else {
		if _, ok := c.proj.Config().FindModel(name); !ok {
			return fmt.Errorf("unknown model %q", name)
		}
		c.roleModels[r] = name
	}
	c.publishRoleModelsLocked()
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
	config, err := c.reloadConfig()
	if err != nil {
		return err
	}
	cfg := resolveModelConfig(config, name)
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
	c.publishRoleModelsLocked()
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
	c.publishRoleModelsLocked()
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
	return c.proj.SessionDir()
}

func (c *EngineController) Resume(id string) (cwdWarning string, err error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("empty session id")
	}
	cfg := c.modelSnapshot()
	c.Cancel()
	eng, sess, cfg, err := c.openEngine(cfg, id)
	if err != nil {
		return "", err
	}
	if sessCwd, cwd := sess.Cwd(), c.proj.Root(); sessCwd != "" && sessCwd != cwd {
		cwdWarning = fmt.Sprintf("cwd %s ≠ %s", filepath.Base(sessCwd), filepath.Base(cwd))
	}
	c.activateSession(eng, sess, cfg)
	return cwdWarning, nil
}

// NewSession starts a brand-new persisted session. Caller must ensure no agent
// stream / local bash is in flight.
func (c *EngineController) NewSession() error {
	cfg := c.modelSnapshot()
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
	c.sessionMu.RLock()
	forked, err := c.session.Fork()
	c.sessionMu.RUnlock()
	if err != nil {
		return "", err
	}
	cfg := c.modelSnapshot()
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
	c.jobs.SetParent(sess.ID(), sess.Cwd(), 0)
	c.publishRoleModelsLocked()
	c.sessionMu.Unlock()
}

// discardFork drops a fork the controller never switched to, so a failed switch
// does not leave an unreachable session in the session list.
func (c *EngineController) discardFork(id string) {
	if _, err := session.DeleteSession(c.SessionDir(), id); err != nil {
		debuglog.Logf("session: drop unused fork %s: %v", id, err)
	}
}

func (c *EngineController) reloadConfig() (*project.Config, error) {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	if err := c.proj.LoadConfig(); err != nil {
		return nil, err
	}
	return c.proj.Config(), nil
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
		agent.WithCwd(c.proj.Root()),
		agent.WithSessionDir(c.SessionDir()),
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
		c.sessionMu.RLock()
		cfg = resolveModelConfig(c.proj.Config(), name)
		c.sessionMu.RUnlock()
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
		jobs = agenttool.BindForSession(c.jobs, sess.Cwd())
	}
	toolList := tools.DefaultTools(c.mcpPool)
	if jobs != nil {
		toolList = slices.Concat(toolList, jobs.Tools())
	}
	system := prompt.Build(sess.Cwd(), cfg.SkillPaths, jobs != nil, jobs.MaxConcurrent(), c.mcpPool.ServerNames())
	client := llmclient.NewClient(cfg, model.HooksForConfig(cfg), tooldef.Definitions(toolList), system)
	executor := agent.NewExecutor(tooldef.NewRegistry(toolList), sess.Cwd())
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
	c.closeOnce.Do(func() {
		c.Cancel()
		if c.jobs != nil {
			_ = c.jobs.Close(context.Background())
		}
		if c.mcpPool != nil {
			_ = c.mcpPool.Close()
			c.mcpPool = nil
		}
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
