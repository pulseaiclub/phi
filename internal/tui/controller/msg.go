package controller

import (
	"time"

	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/job"
	"github.com/pulseaiclub/phi/internal/permission"
	"github.com/pulseaiclub/phi/internal/session"
)

// Msg is a UI-thread message. Producers send; Editor.Update applies.
// Share memory by communicating — not the other way around.
type Msg interface {
	isMsg()
}

// SubmitMsg asks the UI to accept a user prompt.
type SubmitMsg struct{ Text string }

func (SubmitMsg) isMsg() {}

// CancelStreamMsg aborts the in-flight agent stream.
type CancelStreamMsg struct{}

func (CancelStreamMsg) isMsg() {}

// SessionEventMsg carries a session model event from the agent pipeline.
type SessionEventMsg struct {
	Event session.Event
	Gen   int
}

func (SessionEventMsg) isMsg() {}

// FooterKind discriminates variants of FooterMsg.
type FooterKind int

const (
	FooterSetActivity FooterKind = iota
	FooterClearIfActivity
	FooterUpdateAvailable
)

// FooterMsg drives footer activity status and update hints.
type FooterMsg struct {
	Gen  int
	Kind FooterKind

	Activity Activity // FooterSetActivity
	If       Activity // FooterClearIfActivity — Idle only when current still matches

	Latest  string // FooterUpdateAvailable
	Current string
}

func (FooterMsg) isMsg() {}

// RedrawMsg only asks for a frame (e.g. delayed activity clear).
type RedrawMsg struct{}

func (RedrawMsg) isMsg() {}

// ToastMsg asks the Editor to show a transient overlay notification.
// Producers Publish this instead of holding a toast callback.
type ToastMsg struct {
	Message  string
	Kind     toast.ToastKind
	Duration time.Duration
}

func (ToastMsg) isMsg() {}

// ThemeMsg asks the Editor to switch the UI chrome theme by name.
type ThemeMsg struct{ Name string }

func (ThemeMsg) isMsg() {}

// MentionResultsMsg delivers async @-file search results to the UI goroutine.
type MentionResultsMsg struct {
	Gen   int
	Query string
	Paths []string
	// Truncated reports that more matches exist than Paths holds, so the
	// picker can say the list is partial instead of looking complete.
	Truncated bool
	ErrText   string
}

func (MentionResultsMsg) isMsg() {}

// OverlayKind discriminates ask/dismiss variants of OverlayMsg.
type OverlayKind int

const (
	OverlayPermissionAsk OverlayKind = iota
	OverlayPermissionDismiss
	OverlayContinueAsk
	OverlayContinueDismiss
	OverlayExtConfirm
	OverlayExtConfirmDismiss
)

// OverlayMsg drives permission / continue / extension-confirm UI.
// Kind selects which fields are meaningful; reply chans must be buffered(1).
type OverlayMsg struct {
	Kind OverlayKind

	// Permission ask
	Request   permission.Request
	Reason    string
	PermReply chan AskReply

	// Continue ask
	MaxRounds int
	ContReply chan ContinueReply

	// Extension confirm
	Title        string
	Message      string
	Yes          string
	No           string
	Danger       bool
	ConfirmReply chan ExtConfirmReply
}

func (OverlayMsg) isMsg() {}

// ExtCommandResultMsg delivers the result of an extension slash command.
type ExtCommandResultMsg struct {
	Gen       uint64
	Submit    string
	Toast     string
	Status    string
	StatusSet bool
	Err       string
}

func (ExtCommandResultMsg) isMsg() {}

// ExtSessionEffectsMsg applies toast/status from session lifecycle extensions.
type ExtSessionEffectsMsg struct {
	Toast     string
	Status    string
	StatusSet bool
}

func (ExtSessionEffectsMsg) isMsg() {}

// JobProgressMsg carries a live sub-agent tool update for the nested tree UI.
type JobProgressMsg struct {
	Progress job.Progress
}

func (JobProgressMsg) isMsg() {}

// BranchLabelMsg refreshes the path label's git branch after an external
// checkout (e.g. from another terminal or editor).
type BranchLabelMsg struct {
	Text string
}

func (BranchLabelMsg) isMsg() {}
