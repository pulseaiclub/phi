package editor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/agent/prompt"
	"github.com/pulseaiclub/phi/internal/job"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/project"
	projectmodel "github.com/pulseaiclub/phi/internal/project/model"
	"github.com/pulseaiclub/phi/internal/session/compaction"
	"github.com/pulseaiclub/phi/internal/tools"
	"github.com/pulseaiclub/phi/internal/tools/agenttool"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

func newInjectedTestController(t *testing.T, bus *controller.Bus, proj *project.Project) *controller.EngineController {
	t.Helper()
	require.NoError(t, proj.LoadConfig())
	config := proj.Config()
	pool := mcp.NewPool(nil)
	var jobs *job.Manager
	cleanup := true
	t.Cleanup(func() {
		if cleanup {
			if jobs != nil {
				_ = jobs.Close(context.WithoutCancel(t.Context()))
			}
			_ = pool.Close()
		}
	})
	modelConfig := config.Model()
	sess, err := agent.NewSession(
		agent.WithCwd(proj.Root()),
		agent.WithSessionDir(proj.SessionDir()),
		agent.WithPersist(true),
		agent.WithModel(modelConfig.Name),
	)
	require.NoError(t, err)
	jobs, err = job.New(
		proj.JobsDir(),
		agent.NewEngineRunner(agent.DefaultChildSpecs()),
		job.WithParent(sess.ID(), sess.Cwd(), 0),
		job.WithRoleModels(config.ResolveRoleModels(modelConfig, config.Agents.Models.RoleOverrides())),
	)
	require.NoError(t, err)
	var binding *agenttool.Binding
	if config.Agents.Enabled {
		binding = agenttool.BindForSession(jobs, sess.Cwd())
	}
	toolList := tools.DefaultTools(pool)
	toolList = append(toolList, binding.Tools()...)
	system := prompt.Build(
		sess.Cwd(),
		modelConfig.SkillPaths,
		config.Agents.Enabled,
		binding.MaxConcurrent(),
		pool.ServerNames(),
	)
	client := llmclient.NewClient(
		modelConfig,
		projectmodel.HooksForConfig(modelConfig),
		tooldef.Definitions(toolList),
		system,
	)
	executor := agent.NewExecutor(tooldef.NewRegistry(toolList), sess.Cwd())
	engine, err := agent.NewEngine(compaction.LimitsForModel(modelConfig), sess, client, executor)
	require.NoError(t, err)
	ctrl := controller.NewController(bus, proj, sess, jobs, pool, engine)
	cleanup = false
	t.Cleanup(ctrl.Close)
	return ctrl
}
