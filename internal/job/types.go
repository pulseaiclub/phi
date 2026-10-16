package job

import (
	"fmt"
	"time"

	"github.com/pulseaiclub/phi/internal/permission"
)

// Status is the lifecycle state of a job.
type Status string

// Job lifecycle status values.
const (
	// StatusStarting means the job was accepted and a goroutine is about to run.
	// There is no wait queue: when MaxConcurrent slots are full, Spawn returns
	// [ErrBusy] instead of enqueueing.
	StatusStarting  Status = "starting"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
	StatusTimedOut  Status = "timed_out"
)

// Terminal reports whether no further work will run.
func (s Status) Terminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCancelled, StatusTimedOut:
		return true
	default:
		return false
	}
}

// Meta is persisted as meta.json.
type Meta struct {
	ID          string    `json:"id"`
	ParentID    string    `json:"parent_id,omitempty"`
	ParentDepth int       `json:"parent_depth"`
	Role        Role      `json:"role,omitempty"` // explore | worker | review; empty → explore
	Prompt      string    `json:"prompt"`
	Description string    `json:"description,omitempty"`
	WorkDir     string    `json:"workdir,omitempty"`
	Status      Status    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	FinishedAt  time.Time `json:"finished_at,omitempty"`
	Error       string    `json:"error,omitempty"`
	ResultPath  string    `json:"result_path,omitempty"`
	EventsPath  string    `json:"events_path,omitempty"`
	Dir         string    `json:"dir"`
}

// Info is a list/get snapshot.
type Info struct {
	Meta
}

// SpawnRequest configures a new job in memory; JSON tool arguments use [SpawnArgs].
type SpawnRequest struct {
	Prompt          string
	Description     string
	ParentID        string // parent session or parent job id (opaque to this package)
	ParentToolUseID string // parent agent tool_use id for TUI nesting (not persisted)
	Depth           int    // 0 = top-level; tool layer should force Depth for children
	Role            Role   // explore | worker | review; empty → explore
	WorkDir         string
	Timeout         time.Duration   // 0 = no run timeout; Cancel still works
	Gate            permission.Gate // trusted parent constraint; never persisted
}

func (r SpawnRequest) validate() error {
	if r.Prompt == "" {
		return fmt.Errorf("%w: prompt is required", ErrInvalid)
	}
	if _, err := ParseRole(string(r.Role)); err != nil {
		return err
	}
	return nil
}

// Event is one JSONL log line.
type Event struct {
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}

// WaitResult is returned by Wait / Task.
type WaitResult struct {
	Info    Info
	Summary string // contents of result.md when present
}

// RecoveryMode controls how [New] treats leftover non-terminal jobs on disk.
type RecoveryMode int

const (
	// RecoverMarkFailed marks starting/running jobs as failed (default).
	// Safe for TUI / CLI restarts so Wait/Cancel do not see zombies.
	RecoverMarkFailed RecoveryMode = iota
	// RecoverIgnore leaves disk state unchanged (tests / manual inspection).
	RecoverIgnore
)
