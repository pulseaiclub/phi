package controller

import (
	"fmt"

	"github.com/pulseaiclub/phi/internal/session"
)

// Activity mirrors session status for the composer status slot (driven by the stream pipeline).
type Activity int

// Activity values map to status-slot messages shown while the pipeline runs.
const (
	ActivityIdle Activity = iota
	ActivitySubmitting
	ActivityWaiting
	ActivityStreaming
	ActivityTools
	ActivityRetrying
	ActivityCancelled
	ActivityCompacting
	ActivityAwaitingApproval
)

// ActivityHandler owns stream activity state.
// It only mutates itself when Apply / SyncFromSnap are called on the UI goroutine.
type ActivityHandler struct {
	Current  Activity
	onChange func()
}

// NewActivityHandler builds an ActivityHandler.
func NewActivityHandler() *ActivityHandler {
	return &ActivityHandler{}
}

// SetOnChange registers a UI-thread callback after activity changes.
func (h *ActivityHandler) SetOnChange(fn func()) {
	h.onChange = fn
}

func (h *ActivityHandler) notify() {
	if h.onChange != nil {
		h.onChange()
	}
}

// Apply sets activity from a FooterMsg (or direct call on UI thread).
func (h *ActivityHandler) Apply(a Activity) {
	h.Current = a
	h.notify()
}

// SyncFromSnap derives activity from the session snapshot after model updates.
// Upgrades (Idle→Streaming/Tools/…) go through Apply, which notifies.
// The Idle fallback below sets Current directly, so it notifies explicitly.
func (h *ActivityHandler) SyncFromSnap(snap session.Snapshot) {
	// Don't clobber approval activity while the confirmation UI is up.
	if h.Current == ActivityAwaitingApproval {
		return
	}
	if snap.Compacting {
		if h.Current != ActivityCompacting {
			h.Apply(ActivityCompacting)
		}
		return
	}
	if session.HasRunningTools(snap) {
		if h.Current != ActivityTools {
			h.Apply(ActivityTools)
		}
		return
	}
	if session.IsStreaming(snap) {
		if h.Current != ActivityStreaming && h.Current != ActivityWaiting &&
			h.Current != ActivitySubmitting && h.Current != ActivityCompacting {
			h.Apply(ActivityStreaming)
		}
		return
	}
	switch h.Current {
	case ActivityStreaming, ActivityWaiting, ActivitySubmitting, ActivityTools, ActivityCompacting:
		h.Current = ActivityIdle
		h.notify()
	}
}

// ShowSpinner reports whether the current activity animates a spinner.
func (h *ActivityHandler) ShowSpinner() bool {
	return h.Current.showSpinner()
}

// TurnPending reports whether a request is in flight with nothing streamed yet:
// the window between submit and the first token. The transcript has no assistant
// output there, so anything gating on the snapshot alone would miss an Esc that
// arrived while the provider was still thinking.
func (h *ActivityHandler) TurnPending() bool {
	switch h.Current {
	case ActivitySubmitting, ActivityWaiting, ActivityStreaming, ActivityRetrying:
		return true
	default:
		return false
	}
}

// Label returns the status-slot text for the current activity and session snapshot.
func (h *ActivityHandler) Label(snap session.Snapshot) string {
	if h.Current == ActivityTools {
		n := session.RunningToolCount(snap)
		if n > 1 {
			return fmt.Sprintf("Calling %d tools…", n)
		}
	}
	return activityMessage(h.Current)
}

func activityMessage(a Activity) string {
	switch a {
	case ActivitySubmitting:
		return "Sending…"
	case ActivityWaiting:
		return "Awaiting reply…"
	case ActivityStreaming:
		return "Generating…"
	case ActivityTools:
		return "Calling tools…"
	case ActivityCompacting:
		return "Auto-compacting…"
	case ActivityRetrying:
		return "Retrying after disconnect…"
	case ActivityCancelled:
		return "Stopped"
	case ActivityAwaitingApproval:
		return "Waiting for approval…"
	default:
		return ""
	}
}

func (a Activity) showSpinner() bool {
	switch a {
	case ActivitySubmitting, ActivityWaiting, ActivityStreaming, ActivityTools, ActivityRetrying, ActivityCompacting:
		return true
	default:
		return false
	}
}
