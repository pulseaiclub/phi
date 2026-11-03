package controller

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/project/model"
	"github.com/pulseaiclub/phi/internal/session"
)

// This file owns session switching: resume/new teardown, engine construction,
// and the model restore that rides along with a resumed session.

func (c *EngineController) SessionID() string {
	return c.engine.SessionID()
}

// AppendUserBash records a user "!!cmd" run in the current session so the next
// turn includes its output. sessionID guards a session switch that happened
// while the command was running: the output belongs to the old session, not
// whichever one is open now.
func (c *EngineController) AppendUserBash(sessionID, command, output string, exitCode int, cancelled bool) error {
	engine := c.engine
	if engine == nil || engine.Session() == nil {
		return nil
	}
	if sessionID != "" && engine.SessionID() != sessionID {
		return nil
	}
	return engine.Session().AppendBash(command, output, exitCode, cancelled)
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

	c.swapExtensionRunner(c.asm.ReloadExtensions())
	eng, cfg, err := c.openEngine(cfg, id)
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
	prevID, err := c.beginSessionSwitch("new", "", false)
	if err != nil {
		return err
	}
	cfg, err := c.resolveModel()
	if err != nil {
		return err
	}
	engine, cfg, err := c.openEngine(cfg, "")
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

// openEngine builds a session + engine over the shared Assembly collaborators,
// binding the TUI's gate and ask RPCs.
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
		cfg, err = resolveModelConfig(c.proj.Config(), c.asm.Ext(), name)
		if err != nil {
			return nil, llm.ModelConfig{}, fmt.Errorf("restore session model %q: %w", name, err)
		}
		cfg.MaxRounds = c.MaxRounds()
	}
	eng, err := agent.NewEngine(cfg, sess, c.asm.EngineOptions(c.gate, c.agentsEnabled.Load(),
		agent.WithAsk(c.askPermission),
		agent.WithContinueAsk(c.askContinue),
		agent.WithHooks(model.HooksForConfig(cfg)),
	)...)
	if err != nil {
		return nil, llm.ModelConfig{}, err
	}
	return eng, cfg, nil
}

func (c *EngineController) ReplaySnapshot() session.Snapshot {
	if c.engine == nil || c.engine.Session() == nil {
		return session.Snapshot{}
	}
	return session.ReplaySnapshot(c.engine.Session().PathEntries(), c.engine.ToolDetail)
}
