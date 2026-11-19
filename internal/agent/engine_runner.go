package agent

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strings"

	"github.com/pulseaiclub/phi/internal/agent/prompt"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
	"github.com/pulseaiclub/phi/internal/project/model"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/session/compaction"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
)

// EngineRunner runs a child [Engine.Loop] as a [job.Runner].
//
// Each Run creates a fresh Engine with a persisted session under <job.Dir>/session/.
// Child engines do not receive Jobs, so they have no agent_* tools.
type EngineRunner struct {
	specs map[job.Role]ChildSpec
}

// NewEngineRunner returns a runner with owned child specifications.
func NewEngineRunner(specs map[job.Role]ChildSpec) EngineRunner {
	owned := make(map[job.Role]ChildSpec, len(specs))
	maps.Copy(owned, specs)
	return EngineRunner{specs: owned}
}

// Run implements [job.Runner].
func (r EngineRunner) Run(ctx context.Context, env job.RunEnv) (string, error) {
	if env.Job.Dir == "" {
		return "", errors.New("agent: EngineRunner requires job Dir")
	}

	role := job.NormalizeRole(string(env.Job.Role))
	spec, ok := r.specs[role]
	if !ok {
		return "", fmt.Errorf("agent: no child spec for role %q", role)
	}
	sess, err := newJobSession(env.Job)
	if err != nil {
		return "", err
	}
	engine, err := newEngine(env.Model, spec, sess)
	if err != nil {
		return "", err
	}

	env.Log(fmt.Sprintf("sub-agent role=%s session=%s parent=%s", spec.Role, sess.ID(), env.Job.ParentID))

	prompt := env.Job.Prompt
	if env.Job.Description != "" {
		prompt = env.Job.Description + "\n\n" + prompt
	}
	prompt = prompt + "\n\n" + spec.Hint

	var (
		lastText string
		lastErr  error
	)
	for ev, loopErr := range engine.Loop(ctx, prompt, LoopOpts{}) {
		if loopErr != nil {
			lastErr = loopErr
			env.Log("error: " + loopErr.Error())
			break
		}
		switch e := ev.(type) {
		case session.AssistantMessageUpdate:
			if e.Message.State == session.StateComplete {
				if t := strings.TrimSpace(e.Message.FlatText()); t != "" {
					lastText = t
				}
			}
		case session.ToolData:
			detail := e.Run.Detail
			if detail == "" {
				detail = e.Run.Name
			}
			env.Log(fmt.Sprintf("tool %s %s: %s", e.Run.Name, e.Run.Status, detail))
			if env.OnProgress != nil {
				env.OnProgress(job.Progress{
					ToolUseID: e.Run.ToolUseID,
					Name:      e.Run.Name,
					Status:    e.Run.Status.String(),
					Detail:    detail,
				})
			}
		}
	}

	if path := sess.File(); path != "" {
		env.Log("session_file=" + path)
	}

	if lastErr != nil {
		if lastText != "" {
			_ = env.WriteResult(lastText)
		}
		return lastText, lastErr
	}
	if ctx.Err() != nil {
		return lastText, ctx.Err()
	}
	if lastText == "" {
		lastText = "sub-agent finished with no text reply"
	}
	return lastText, nil
}

func newJobSession(meta job.Meta) (*Session, error) {
	cwd := meta.WorkDir
	if cwd == "" {
		cwd = "."
	}
	return NewSession(
		WithCwd(cwd),
		WithSessionDir(filepath.Join(meta.Dir, "session")),
		WithPersist(true),
		WithParentID(meta.ParentID),
	)
}

func newEngine(cfg llm.ModelConfig, spec ChildSpec, sess *Session) (*Engine, error) {
	hooks := model.HooksForConfig(cfg)
	client := llmclient.NewClient(
		cfg,
		hooks,
		tooldef.Definitions(spec.Tools),
		prompt.Build(sess.Cwd(), cfg.SkillPaths, false, 0, nil),
	)
	executor := NewExecutor(tooldef.NewRegistry(spec.Tools), sess.Cwd())
	return NewEngine(compaction.LimitsForModel(cfg), sess, client, executor)
}
