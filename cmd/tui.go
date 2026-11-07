package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/app"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/editor"
)

// runTUI starts the interactive terminal UI (default entrypoint).
func runTUI() error {
	proj := project.GetDefaultProject()
	if err := proj.LoadConfig(); err != nil {
		if !project.SetupNeeded(err) {
			// A broken config is a real error; the dialog must not touch it.
			fmt.Fprintln(os.Stderr, "phi:", err)
			fmt.Fprintln(os.Stderr, "Run phi config to inspect and fix it.")
			return exitCode(ExitUsage)
		}
		// Fresh install: collect the two values phi cannot derive itself.
		switch err := runOnboarding(proj); {
		case errors.Is(err, errOnboardAbort):
			fmt.Fprintln(os.Stderr, "Setup cancelled. Configure a model first, then restart:")
			fmt.Fprintln(os.Stderr, "  phi config")
			fmt.Fprintln(os.Stderr, "or set PHI_MODEL and PHI_API_KEY.")
			return exitCode(ExitUsage)
		case err != nil:
			fmt.Fprintln(os.Stderr, "phi:", err)
			return exitCode(ExitError)
		}
		if err := proj.LoadConfig(); err != nil {
			// The dialog validates what it saves; this only fires on surprises.
			fmt.Fprintln(os.Stderr, "phi:", err)
			return exitCode(ExitUsage)
		}
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
	ctrl, err := controller.NewController(bus, proj)
	if err != nil {
		fmt.Fprintln(os.Stderr, "phi:", err)
		return exitCode(ExitError)
	}
	defer ctrl.Close()

	// Extension models load with the controller; they shadow same-name config
	// entries, so the palette lists each connection once.
	if r := ctrl.Extensions(); r != nil {
		for _, route := range r.ExtensionModels() {
			if !slices.Contains(modelNames, route.Reg.Name) {
				modelNames = append(modelNames, route.Reg.Name)
			}
		}
	}

	model := ctrl.ModelName()
	if think := ctrl.ThinkLevel(); think != "off" && think != "" {
		model = fmt.Sprintf("%s::%s", model, think)
	}

	ui := editor.NewEditor(
		application,
		bus,
		ctrl,
		vx,
		th,
		cwd,
		model,
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
