package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/app"
	"github.com/pulseaiclub/phi/internal/project"
	"github.com/pulseaiclub/phi/internal/tui/onboard"
)

// errOnboardAbort means the user backed out of the first-run dialog.
var errOnboardAbort = errors.New("setup cancelled")

// runOnboarding shows the two-field first-run dialog (model name + API key)
// and saves what it collects as the default model. The fields are prefilled
// from the environment, so a user with PHI_API_KEY set only types the model
// name, and the other way around.
func runOnboarding(proj *project.Project) error {
	vx, err := xui.New(xui.Options{Mouse: true, BracketedPaste: true})
	if err != nil {
		return fmt.Errorf("terminal UI: %w", err)
	}
	defer func() {
		if err := vx.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "phi: close terminal:", err)
		}
	}()

	application := app.NewApp(vx)
	dialog := onboard.New(components.DefaultTheme(), os.Getenv("PHI_MODEL"), os.Getenv("PHI_API_KEY"))
	if err := application.Run(dialog); err != nil {
		return fmt.Errorf("onboarding UI: %w", err)
	}
	// Aborted covers Esc; "loop ended without a submit" is the app-level
	// Ctrl+C quit, and both get the same friendly advice instead of a save
	// error on empty values.
	if dialog.Aborted() || !dialog.Submitted() {
		return errOnboardAbort
	}
	return project.UpsertDefaultModel(proj.Global(), dialog.Name(), dialog.APIKey())
}
