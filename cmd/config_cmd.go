package main

import (
	"fmt"
	"os"

	"github.com/pulseaiclub/xui"

	cli "github.com/pulseaiclub/pli"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/app"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/tui/configui"
)

var configCommand = cli.Command{
	Name: "config",
	Desc: "edit the meta config in a terminal UI",
	Long: `Edit the meta config in a full-screen terminal UI (models, skills,
permissions, sub-agents). Changes are written only on save; the previous file
is kept next to it as config.yaml.bak.`,
	Run: func(_ []string, _ cli.Flags) error {
		return runConfigEditor()
	},
}

// runConfigEditor opens ~/.phi/config.yaml in the config form.
func runConfigEditor() error {
	proj := project.GetDefaultProject()
	path := proj.Global().ConfigFile()

	doc, err := project.ReadConfigDoc(path)
	if err != nil {
		return err
	}

	vx, err := xui.New(xui.Options{Mouse: true, BracketedPaste: true})
	if err != nil {
		fmt.Fprintln(os.Stderr, "phi: terminal UI:", err)
		return exitCode(ExitError)
	}
	defer func(vx *xui.XUI) {
		if err := vx.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "phi: close terminal:", err)
		}
	}(vx)

	application := app.NewApp(app.WithUI(vx))
	ui := configui.New(
		doc,
		path,
		proj.Global().SkillsDir(),
		components.DefaultTheme(),
		fetchModelIDs,
		application.RequestRedraw,
	)
	if err := application.Run(ui); err != nil {
		fmt.Fprintln(os.Stderr, "phi:", err)
		return exitCode(ExitError)
	}
	return nil
}
