package main

import (
	"context"
	"fmt"
	"os"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/agent"
	"github.com/pulseaiclub/phi/internal/agent/prompt"
	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/app"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/llm"
	llmclient "github.com/pulseaiclub/phi/internal/llm/client"
	"github.com/pulseaiclub/phi/internal/mcp"
	"github.com/pulseaiclub/phi/internal/project"
	projectmodel "github.com/pulseaiclub/phi/internal/project/model"
	"github.com/pulseaiclub/phi/internal/session/compaction"
	"github.com/pulseaiclub/phi/internal/tools"
	"github.com/pulseaiclub/phi/internal/tools/agenttool"
	"github.com/pulseaiclub/phi/internal/tools/tooldef"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/editor"
)

// runTUI starts the interactive terminal UI (default entrypoint).
func runTUI() error {
	proj := project.GetDefaultProject()
	if err := proj.LoadConfig(); err != nil {
		fmt.Fprintln(os.Stderr, "phi:", err)
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Configure a model first, then restart:")
		fmt.Fprintln(os.Stderr, "  phi config")
		fmt.Fprintln(os.Stderr, "or set PHI_MODEL and PHI_API_KEY.")
		return exitCode(ExitUsage)
	}

	// Download fd/rg in the background so a cold install does not block the
	// first TUI frame. Failures stay non-fatal (tools fall back to PATH).
	go func() {
		if err := EnsureSearchTools(context.Background(), proj); err != nil {
			fmt.Fprintln(os.Stderr, "warning: could not install search tools:", err)
		}
	}()

	vx, err := xui.New(xui.Options{Mouse: true, BracketedPaste: true})
	if err != nil {
		fmt.Fprintln(os.Stderr, "phi: terminal UI:", err)
		return exitCode(ExitError)
	}
	defer func(vx *xui.XUI) {
		err := vx.Close()
		if err != nil {
			panic(err)
		}
	}(vx)

	models := proj.Config().AllModels()
	modelNames := make([]string, 0, len(models))
	for _, m := range models {
		modelNames = append(modelNames, m.Name)
	}

	model := proj.Config().Model()
	modelName := model.Name
	if think := model.Think.Mode; think != "off" && think != "" {
		modelName = fmt.Sprintf("%s::%s", model.Name, string(think))
	}

	redraw := controller.NewRedrawRelay()
	bus := controller.NewBus(redraw.Fire)

	sess, err := newSession(proj, model)
	if err != nil {
		fmt.Fprintln(os.Stderr, "phi:", err)
		return exitCode(ExitError)
	}
	mcpPool := newMCPPool(proj)
	jobs, err := newJobs(proj, model, sess)
	if err != nil {
		_ = mcpPool.Close()
		fmt.Fprintln(os.Stderr, "phi:", err)
		return exitCode(ExitError)
	}
	engine, err := newEngine(proj, model, sess, jobs, mcpPool)
	if err != nil {
		_ = jobs.Close(context.Background())
		_ = mcpPool.Close()
		fmt.Fprintln(os.Stderr, "phi:", err)
		return exitCode(ExitError)
	}
	ctrl := controller.NewController(bus, proj, sess, jobs, mcpPool, engine)
	defer ctrl.Close()

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "phi: getwd:", err)
		return exitCode(ExitError)
	}

	application := app.NewApp(vx, app.WithAnim(true))
	ui := editor.NewEditor(
		application,
		bus,
		ctrl,
		vx,
		components.DefaultTheme(),
		cwd,
		modelName,
		proj.Config().SkillPaths,
		modelNames,
	)
	redraw.Bind(ui.RequestRedraw)
	ui.StartUpdateCheck(proj.Global().Root())
	ui.StartBranchWatch()
	if err := application.Run(ui); err != nil {
		fmt.Fprintln(os.Stderr, "phi:", err)
		return exitCode(ExitError)
	}
	return nil
}

func newMCPPool(proj *project.Project) *mcp.Pool {
	pool, err := mcp.LoadPool(proj.MCPConfigFile())
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: mcp:", err)
		return mcp.NewPool(nil)
	}
	if pool == nil {
		return mcp.NewPool(nil)
	}
	return pool
}

func newJobs(proj *project.Project, model llm.ModelConfig, sess *agent.Session) (*job.Manager, error) {
	config := proj.Config()
	models := config.ResolveRoleModels(model, config.Agents.Models.RoleOverrides())
	return job.New(proj.JobsDir(), agent.NewEngineRunner(agent.DefaultChildSpecs()),
		job.WithParent(sess.ID(), sess.Cwd(), 0), job.WithRoleModels(models))
}

func newSession(proj *project.Project, model llm.ModelConfig) (*agent.Session, error) {
	return agent.NewSession(
		agent.WithCwd(proj.Root()),
		agent.WithSessionDir(proj.SessionDir()),
		agent.WithPersist(true),
		agent.WithModel(model.Name),
	)
}

func newJobBinding(proj *project.Project, jobs *job.Manager, sess *agent.Session) *agenttool.Binding {
	if !proj.Config().Agents.Enabled {
		return nil
	}
	return agenttool.BindForSession(jobs, sess.Cwd())
}

func newEngine(
	proj *project.Project,
	model llm.ModelConfig,
	sess *agent.Session,
	jobs *job.Manager,
	mcpPool *mcp.Pool,
) (*agent.Engine, error) {
	jobBinding := newJobBinding(proj, jobs, sess)
	toolList := tools.DefaultTools(mcpPool)
	toolList = append(toolList, jobBinding.Tools()...)
	system := prompt.Build(
		sess.Cwd(),
		model.SkillPaths,
		proj.Config().Agents.Enabled,
		jobBinding.MaxConcurrent(),
		mcpPool.ServerNames(),
	)
	client := llmclient.NewClient(model, projectmodel.HooksForConfig(model), tooldef.Definitions(toolList), system)
	executor := agent.NewExecutor(tooldef.NewRegistry(toolList), sess.Cwd())
	return agent.NewEngine(compaction.LimitsForModel(model), sess, client, executor)
}
