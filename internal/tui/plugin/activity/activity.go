// Package activity owns the stream activity state machine behind the composer
// status slot (labels, spinner visibility, turn-pending window). It applies
// FooterMsg activity updates and exposes the narrow surface the shell and
// other plugins read.
package activity

import (
	"fmt"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/layout"
	"github.com/pulseaiclub/phi/internal/components/status"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/footer"
	"github.com/pulseaiclub/phi/internal/tui/runtime"
)

// LabelSink receives the composer status slot (bottom-left label).
type LabelSink interface {
	SetBottomLeftLabel(layout.BorderLabel)
	ClearBottomLeftLabel()
}

// Deps carries the collaborators the root wires as method values.
type Deps struct {
	// Snapshot reads the session snapshot for labels and tool counts.
	Snapshot func() session.Snapshot
	// ContextWindow reads the model context window for the usage readout.
	ContextWindow func() int
	// Labels is the composer status slot.
	Labels LabelSink
}

// Plugin owns the activity state, the spinner cadence, and the composer
// status slot. It only mutates itself when Apply / SyncFromSnap are called
// on the UI goroutine.
type Plugin struct {
	current  controller.Activity
	onChange func()
	theme    components.Theme
	spin     *status.Spinner
	deps     Deps

	spinTick int
}

// New builds the activity plugin around the shell's shared spinner and seeds
// the status slot, so the first frame already shows the current activity.
func New(theme components.Theme, spin *status.Spinner, deps Deps) *Plugin {
	p := &Plugin{theme: theme, spin: spin, deps: deps}
	if deps.Snapshot != nil && deps.ContextWindow != nil && deps.Labels != nil {
		p.SyncStatusSlot()
	}
	return p
}

// Register wires the plugin into the runtime: FooterMsg activity updates,
// theme switches, and the per-frame spinner tick.
func (p *Plugin) Register(rt *runtime.Runtime) {
	runtime.Subscribe(rt, p.onFooterMsg)
	rt.Theme.On(p.SetTheme)
	rt.Clock.On(p.Frame)
}

func (p *Plugin) onFooterMsg(msg controller.FooterMsg) {
	switch msg.Kind {
	case controller.FooterSetActivity:
		p.Apply(msg.Activity)
	case controller.FooterClearIfActivity:
		if p.current == msg.If {
			p.Apply(controller.ActivityIdle)
		}
	}
}

// SetOnChange registers an extra UI-thread callback after activity changes.
// The spinner reset and status-slot refresh always run; the callback is
// additional.
func (p *Plugin) SetOnChange(fn func()) {
	p.onChange = fn
}

func (p *Plugin) notify() {
	p.spin.Frame = 0
	p.spinTick = 0
	p.SyncStatusSlot()
	if p.onChange != nil {
		p.onChange()
	}
}

// Apply sets activity from a FooterMsg (or direct call on UI thread).
func (p *Plugin) Apply(a controller.Activity) {
	p.current = a
	p.notify()
}

// Current reports the active activity state.
func (p *Plugin) Current() controller.Activity { return p.current }

// SyncFromSnap derives activity from the session snapshot after model updates.
// Upgrades (Idle→Streaming/Tools/…) go through Apply, which notifies.
// The Idle fallback below sets current directly, so it notifies explicitly.
func (p *Plugin) SyncFromSnap(snap session.Snapshot) {
	// Don't clobber approval activity while the confirmation UI is up.
	if p.current == controller.ActivityAwaitingApproval {
		return
	}
	if snap.Compacting {
		if p.current != controller.ActivityCompacting {
			p.Apply(controller.ActivityCompacting)
		}
		return
	}
	if session.HasRunningTools(snap) {
		if p.current != controller.ActivityTools {
			p.Apply(controller.ActivityTools)
		}
		return
	}
	if session.IsStreaming(snap) {
		if p.current != controller.ActivityStreaming && p.current != controller.ActivityWaiting &&
			p.current != controller.ActivitySubmitting && p.current != controller.ActivityCompacting {
			p.Apply(controller.ActivityStreaming)
		}
		return
	}
	switch p.current {
	case controller.ActivityStreaming,
		controller.ActivityWaiting,
		controller.ActivitySubmitting,
		controller.ActivityTools,
		controller.ActivityCompacting:
		p.current = controller.ActivityIdle
		p.notify()
	}
}

// ShowSpinner reports whether the current activity animates a spinner.
func (p *Plugin) ShowSpinner() bool {
	switch p.current {
	case controller.ActivitySubmitting,
		controller.ActivityWaiting,
		controller.ActivityStreaming,
		controller.ActivityTools,
		controller.ActivityRetrying,
		controller.ActivityCompacting:
		return true
	default:
		return false
	}
}

// TurnPending reports whether a request is in flight with nothing streamed yet:
// the window between submit and the first token. The transcript has no assistant
// output there, so anything gating on the snapshot alone would miss an Esc that
// arrived while the provider was still thinking.
func (p *Plugin) TurnPending() bool {
	switch p.current {
	case controller.ActivitySubmitting,
		controller.ActivityWaiting,
		controller.ActivityStreaming,
		controller.ActivityRetrying:
		return true
	default:
		return false
	}
}

// Label returns the status-slot text for the current activity and session snapshot.
func (p *Plugin) Label(snap session.Snapshot) string {
	if p.current == controller.ActivityTools {
		n := session.RunningToolCount(snap)
		if n > 1 {
			return fmt.Sprintf("Calling %d tools…", n)
		}
	}
	return activityMessage(p.current)
}

func activityMessage(a controller.Activity) string {
	switch a {
	case controller.ActivitySubmitting:
		return "Sending…"
	case controller.ActivityWaiting:
		return "Awaiting reply…"
	case controller.ActivityStreaming:
		return "Generating…"
	case controller.ActivityTools:
		return "Calling tools…"
	case controller.ActivityCompacting:
		return "Auto-compacting…"
	case controller.ActivityRetrying:
		return "Retrying after disconnect…"
	case controller.ActivityCancelled:
		return "Stopped"
	case controller.ActivityAwaitingApproval:
		return "Waiting for approval…"
	default:
		return ""
	}
}

// Frame ticks the spinner on the UI frame cadence (~15Hz via the shell's
// frame divisor) and refreshes the slot only while it animates.
func (p *Plugin) Frame() {
	p.spinTick++
	if p.ShowSpinner() && p.spinTick%spinnerFrameDivisor == 0 {
		p.spin.Tick()
		p.SyncStatusSlot()
	}
}

// spinnerFrameDivisor throttles the 60fps app redraw to the status slot's
// ~15Hz spinner cadence. Must match the shell's divisor.
const spinnerFrameDivisor = 4

// SyncStatusSlot recomputes the composer status slot from the current
// activity, spinner frame, and snapshot usage.
func (p *Plugin) SyncStatusSlot() {
	label := footer.StatusLabel(p.theme, footer.StatusInput{
		Activity:      p.Label(p.deps.Snapshot()),
		ShowSpinner:   p.ShowSpinner(),
		Spinner:       p.spin,
		Usage:         p.deps.Snapshot().LastUsage(),
		ContextWindow: p.deps.ContextWindow(),
	})
	if !label.Visible() {
		p.deps.Labels.ClearBottomLeftLabel()
		return
	}
	p.deps.Labels.SetBottomLeftLabel(label)
}

// SetTheme switches the activity chrome and refreshes the slot.
func (p *Plugin) SetTheme(th components.Theme) {
	p.theme = th
	p.spin.Style = th.ToolName
	p.SyncStatusSlot()
}
