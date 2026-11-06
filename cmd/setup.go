package main

import (
	"errors"
	"fmt"
	"io"
)

// setupError marks a failure the user fixes in config.yaml (no model, no api
// key) without changing the message they see. Entrypoints use it to add the
// same next-step hint the TUI prints, so `phi run` is never a dead end.
type setupError struct{ err error }

func (e setupError) Error() string { return e.err.Error() }
func (e setupError) Unwrap() error { return e.err }

func isSetupError(err error) bool {
	var se setupError
	return errors.As(err, &se)
}

// printSetupHint writes the two ways to get a usable model. Callers print the
// error first, so this stays a bare "what now" block.
func printSetupHint(w io.Writer) {
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Configure a model first:")
	fmt.Fprintln(w, "  phi config")
	fmt.Fprintln(w, "or set PHI_MODEL and PHI_API_KEY.")
}
