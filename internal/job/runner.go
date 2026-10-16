package job

import (
	"context"

	"github.com/pulseaiclub/phi/internal/permission"
)

// RunEnv is the in-memory execution context a [Runner] sees for one job.
//
// Log lines are persisted to events.jsonl so [Manager.Log] can stream them.
// If summary is non-empty when Run returns nil, the manager writes result.md.
// WriteResult is also available for mid-run snapshots.
//
// OnProgress (when non-nil) receives structured tool updates for the parent TUI.
// It must not block; the manager may drop events if subscribers are slow.
type RunEnv struct {
	Job         Meta
	Gate        permission.Gate // parent constraint captured for this spawn
	Log         func(message string)
	WriteResult func(summary string) error
	OnProgress  func(Progress)
}

// Runner executes job work. Implementations must respect ctx cancellation.
//
// Returning a non-nil error marks the job failed, unless ctx was cancelled
// ([StatusCancelled]) or hit the spawn timeout ([StatusTimedOut]).
type Runner interface {
	Run(ctx context.Context, env RunEnv) (summary string, err error)
}

// RunnerFunc adapts a function to [Runner].
type RunnerFunc func(ctx context.Context, env RunEnv) (summary string, err error)

// Run calls f with the given context and environment.
func (f RunnerFunc) Run(ctx context.Context, env RunEnv) (string, error) {
	return f(ctx, env)
}
