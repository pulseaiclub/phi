package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pulseaiclub/phi/internal/extension"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/project/model"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tools"
)

// EngineRunner runs a child [Engine.Loop] as a [job.Runner].
//
// Each Run creates a fresh Engine with a persisted session under
// <job.Dir>/session/, ParentID from the job, and no Ask handler.
// Child engines do not receive Jobs, so they have no agent_* tools.
// Role (explore|worker|review) selects tools and default permission mode
// when Gate/Tools are nil. ModelFn (when set) selects the model per role;
// otherwise Model is used as a fixed snapshot.
//
// Extensions (or ExtensionsFn) are inherited from the parent so policy
// tool_call handlers apply. Extension RegisterTool tools are not merged
// into child engines. ExtensionsFn wins when set (live reload).
type EngineRunner struct {
	Model        llm.ModelConfig
	ModelFn      func(role job.Role) llm.ModelConfig // if set, preferred over Model
	Gate         permission.Gate                     // nil → SpecForRole(job.Role).Mode on WorkDir
	Tools        []tools.Tool                        // nil → SpecForRole(job.Role).Tools
	MaxRounds    int                                 // <=0 → model budget or Engine default
	Extensions   *extension.Runner
	ExtensionsFn func() *extension.Runner
}

// Run implements [job.Runner].
func (r EngineRunner) Run(ctx context.Context, env job.RunEnv) (string, error) {
	if env.Job.Dir == "" {
		return "", errors.New("agent: EngineRunner requires job Dir")
	}

	spec := SpecForRole(env.Job.Role)
	engine, err := r.newEngine(env.Job, spec)
	if err != nil {
		return "", err
	}

	env.Log(fmt.Sprintf("sub-agent role=%s session=%s parent=%s", spec.Role, engine.SessionID(), env.Job.ParentID))

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

	if path := engine.SessionFile(); path != "" {
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

func (r EngineRunner) newEngine(meta job.Meta, spec ChildSpec) (*Engine, error) {
	cwd := meta.WorkDir
	if cwd == "" {
		cwd = "."
	}

	gate := r.Gate
	if gate == nil {
		g, err := permission.NewGate(permission.ChildPolicy(spec.Mode), cwd)
		if err != nil {
			return nil, err
		}
		gate = g
	}

	toolList := r.Tools
	if toolList == nil {
		toolList = spec.Tools
	}

	cfg := r.Model
	if r.ModelFn != nil {
		cfg = r.ModelFn(meta.Role)
	}

	extRunner := r.Extensions
	if r.ExtensionsFn != nil {
		extRunner = r.ExtensionsFn()
	}

	sess, err := NewSession(
		WithCwd(cwd),
		WithSessionDir(filepath.Join(meta.Dir, "session")),
		WithPersist(true),
		WithParentID(meta.ParentID),
	)
	if err != nil {
		return nil, err
	}
	return NewEngine(cfg, sess,
		WithGate(gate),
		WithTools(toolList),
		WithMaxRounds(r.MaxRounds),
		WithExtensions(extRunner),
		WithOmitExtensionTools(true),
		WithHooks(model.HooksFor(cfg.Name)),
	)
}
