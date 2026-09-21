package optimizer

import "context"

// Optimizer observes agent actions and may return an advisory decision.
// Implementations must not execute commands or grant permissions.
type Optimizer interface {
	BeforeCommand(context.Context, CommandState) (Decision, error)
	AfterCommand(context.Context, CommandState, CommandResult) (Decision, error)
}

// Nop is the safe default when optimization is disabled or unavailable.
type Nop struct{}

// BeforeCommand allows Nop to satisfy Optimizer without changing execution.
func (Nop) BeforeCommand(context.Context, CommandState) (Decision, error) {
	return Decision{}, nil
}

// AfterCommand allows Nop to satisfy Optimizer without changing execution.
func (Nop) AfterCommand(context.Context, CommandState, CommandResult) (Decision, error) {
	return Decision{}, nil
}

// CommandState describes a command before or after execution.
type CommandState struct {
	Task     string
	Command  string
	CWD      string
	Purpose  string
	Previous []string
}

// CommandResult describes the observable outcome of a command execution.
type CommandResult struct {
	ExitCode int
	Output   string
	TimedOut bool
	Canceled bool
}

// Disposition describes whether an optimizer recommends changing the flow.
type Disposition string

const (
	// DispositionUnchanged is the zero value and leaves normal execution intact.
	DispositionUnchanged Disposition = ""
	// DispositionAllow recommends continuing with the current operation.
	DispositionAllow Disposition = "allow"
	// DispositionDeny recommends skipping the current operation.
	DispositionDeny Disposition = "deny"
)

// Decision is an advisory result from an optimizer. A zero Decision has an
// unchanged disposition and is safe to ignore.
type Decision struct {
	Disposition Disposition
	NeedsReview bool
	Retry       bool
	Stop        bool
	Reason      string
	Confidence  float64
	Category    string
}
