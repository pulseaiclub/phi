package main

import (
	"context"
	"fmt"
	"os"
	"slices"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/debuglog"
	"github.com/pulseaiclub/phi/internal/extension"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/app"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/editor"
)

// loadExtensions discovers ~/.phi/extensions and <cwd>/.phi/extensions.
// Load errors are non-fatal (fail-open: no extensions).
func loadExtensions(proj *project.Project) *extension.Runner {
	r, warns, err := extension.Load(proj.Global().ExtensionsDir(), proj.ExtensionsDir())
	if err != nil {
		debuglog.Logf("extension: load failed: %v", err)
		return nil
	}
	for _, w := range warns {
		debuglog.Logf("extension: %s", w.String())
	}
	return r
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

	extRunner := loadExtensions(proj)
	var extensionModels []extension.ModelRoute
	if extRunner != nil {
		extensionModels = extRunner.ExtensionModels()
	}
	for _, route := range extensionModels {
		if !slices.Contains(modelNames, route.Reg.Name) {
			modelNames = append(modelNames, route.Reg.Name)
		}
	}

	model := proj.Config().Model()
	modelName := model.Name
	if think := model.Think.Mode; think != "off" && think != "" {
		modelName = fmt.Sprintf("%s::%s", model.Name, string(think))
	}

	redraw := controller.NewRedrawRelay()
	bus := controller.NewBus(redraw.Fire)

	ctrl, err := controller.NewController(bus, proj, extRunner)
	if err != nil {
		extRunner.Close()
		fmt.Fprintln(os.Stderr, "phi:", err)
		return exitCode(ExitError)
	}
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
