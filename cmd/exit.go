package main

// Process exit codes (shared by TUI and headless entrypoints).
const (
	ExitOK    = 0 // loop finished without errors
	ExitError = 1 // runtime / LLM / session error
	ExitUsage = 3 // config or CLI usage error
)

// exitError carries a process exit code. Commands that already printed their
// own diagnostics return this so main can os.Exit without a second message.
// It implements silent() so pli does not wrap it in *RunError.
type exitError struct {
	code int
}

func (*exitError) Error() string { return "" }

//nolint:unused // pli checks it through interface{ silent() bool } (cli.go).
func (*exitError) silent() bool { return true }

func exitCode(code int) error {
	if code == ExitOK {
		return nil
	}
	return &exitError{code: code}
}
