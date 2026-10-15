package main

import (
	"context"
	"fmt"
	"os"

	"github.com/pulseaiclub/xui"

	cli "github.com/pulseaiclub/pli"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/app"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/editor"
)

var tuiCommand = cli.Command{
	Name: "tui",
	Desc: "start the interactive TUI",
	Run: func(_ []string, _ cli.Flags) error {
		return runTUI()
	},
}

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
	cfg := proj.Config().Model()

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

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "phi: getwd:", err)
		return exitCode(ExitError)
	}
	th := components.DefaultTheme()
	models := proj.Config().AllModels()
	modelNames := make([]string, 0, len(models))
	for _, m := range models {
		modelNames = append(modelNames, m.Name)
	}

	application := app.NewApp(vx)
	application.Anim = true

	redraw := controller.NewRedrawRelay()
	bus := controller.NewBus(redraw.Fire)
	ctrl, err := controller.NewController(bus, proj, cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "phi:", err)
		return exitCode(ExitError)
	}
	defer ctrl.Close()

	model := cfg.Name
	if cfg.Think.Enabled {
		model = fmt.Sprintf("%s::%s", model, string(cfg.Think.Mode))
	}

	ui := editor.NewEditor(
		application,
		bus,
		ctrl,
		vx,
		th,
		cwd,
		model,
		cfg.SkillPaths,
		cfg.ContextWindow,
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
