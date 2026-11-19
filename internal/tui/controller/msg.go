package controller

import (
	"time"

	"github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/job"
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
	Gen   int // zero for events not owned by a request
}

func (SessionEventMsg) isMsg() {}

// LoopStartedMsg marks a background submission as pending on the UI thread.
type LoopStartedMsg struct{ Gen int }

func (LoopStartedMsg) isMsg() {}

// LoopFinishedMsg ends the current stream before optionally showing an error.
type LoopFinishedMsg struct {
	Gen       int
	ErrorText string
	Cancelled bool
}

func (LoopFinishedMsg) isMsg() {}

// FooterKind discriminates variants of FooterMsg.
type FooterKind int

const (
	FooterSetActivity FooterKind = iota
	FooterClearIfActivity
	FooterUpdateAvailable
)

// FooterMsg drives footer activity status and update hints.
type FooterMsg struct {
	Kind FooterKind

	Activity Activity // FooterSetActivity
	If       Activity // FooterClearIfActivity — Idle only when current still matches

	Latest  string // FooterUpdateAvailable
	Current string
}

func (FooterMsg) isMsg() {}

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

// BashSuggestion is one full-line completion for text typed in "!" mode. Score
// is the engine's ranking weight; IsPrefix distinguishes literal completions
// from fuzzy matches. Description explains the command or argument when known.
type BashSuggestion struct {
	Command     string
	Description string
	Score       float64
	IsPrefix    bool
}

// BashSuggestionsMsg delivers async "!" completions to the UI goroutine. Gen
// and Query let the composer drop a result the composer text has moved past.
type BashSuggestionsMsg struct {
	Gen     int
	Query   string
	Items   []BashSuggestion
	ErrText string
}

func (BashSuggestionsMsg) isMsg() {}

// OverlayKind discriminates ask/dismiss variants of OverlayMsg.
type OverlayKind int

const (
	OverlayErrorAsk OverlayKind = iota
	OverlayErrorDismiss
)

// OverlayMsg drives the loop-error UI.
// Kind selects which fields are meaningful.
type OverlayMsg struct {
	Kind OverlayKind

	ErrorText string
}

func (OverlayMsg) isMsg() {}

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
