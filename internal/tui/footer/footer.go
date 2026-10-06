package footer

import (
	"fmt"
	"strings"

	"github.com/pulseaiclub/xui"

	"github.com/pulseaiclub/phi/internal/components"
	"github.com/pulseaiclub/phi/internal/components/layout"
	"github.com/pulseaiclub/phi/internal/components/status"
	"github.com/pulseaiclub/phi/internal/session"
	"github.com/pulseaiclub/phi/internal/tui/controller"
)

type labelComposer interface {
	SetBottomLeftLabel(layout.BorderLabel)
	ClearBottomLeftLabel()
}

// FooterChrome owns the composer status slot (activity ↔ tokens), spinner,
// and the bottom footer row reserved for extension/ambient chrome.
type FooterChrome struct {
	theme         components.Theme
	spin          *status.Spinner
	activity      *controller.ActivityHandler
	contextWindow int
	lastUsage     session.TokenUsage
	updateHint    string
	hookStatus    string
	tick          int

	composer     labelComposer
	labelContext func() session.Snapshot
	liveJobs     func() int
}

// NewFooterChrome builds footer chrome with a fresh spinner and activity handler.
func NewFooterChrome(theme components.Theme, contextWindow int) *FooterChrome {
	spin := status.NewSpinner(theme.ToolName)
	f := &FooterChrome{
		theme:         theme,
		spin:          spin,
		activity:      controller.NewActivityHandler(spin),
		contextWindow: contextWindow,
	}
	f.activity.SetOnChange(f.syncStatusSlot)
	return f
}

// Spinner returns the shared spinner (e.g. for TranscriptPane mapper).
func (f *FooterChrome) Spinner() *status.Spinner {
	return f.spin
}

// Activity returns the activity handler.
func (f *FooterChrome) Activity() *controller.ActivityHandler {
	return f.activity
}

// BindComposer wires the composer for status-slot updates.
func (f *FooterChrome) BindComposer(c labelComposer) {
	f.composer = c
	f.syncStatusSlot()
}

// SetLabelContext supplies snap for activity status labels.
func (f *FooterChrome) SetLabelContext(fn func() session.Snapshot) {
	f.labelContext = fn
}

// SetLiveJobs supplies live sub-agent job count for the footer row.
func (f *FooterChrome) SetLiveJobs(fn func() int) {
	f.liveJobs = fn
}

// AdvanceTick drives spinner animation during active work.
func (f *FooterChrome) AdvanceTick() {
	f.tick++
	if f.activity.ShowSpinner() && f.tick%4 == 0 {
		f.spin.Tick()
		f.syncStatusSlot()
	}
}

// SyncFromSnap refreshes activity from the session snapshot.
func (f *FooterChrome) SyncFromSnap(snap session.Snapshot) {
	f.activity.SyncFromSnap(snap)
	f.syncStatusSlot()
}

// SetTheme updates footer chrome styling.
func (f *FooterChrome) SetTheme(th components.Theme) {
	f.theme = th
	f.spin.Style = th.ToolName
	f.syncStatusSlot()
}

// UpdateTokenDisplay stores usage and refreshes the status slot. Zero usage
// clears the label, so a session switch never leaves the previous session's
// counts on screen.
func (f *FooterChrome) UpdateTokenDisplay(usage session.TokenUsage) {
	f.lastUsage = usage
	f.syncStatusSlot()
}

// ClearTokenDisplay clears stored usage and refreshes the status slot.
func (f *FooterChrome) ClearTokenDisplay() {
	f.UpdateTokenDisplay(session.TokenUsage{})
}

// SetContextWindow swaps the window used for the context-fill label — the
// startup window belongs to the initial model, so a /model switch must update
// it or every later fill reads against the old window.
func (f *FooterChrome) SetContextWindow(window int) {
	if window == f.contextWindow {
		return
	}
	f.contextWindow = window
	f.syncStatusSlot()
}

// SetExtensionStatus sets the extension status shown on the bottom footer row.
func (f *FooterChrome) SetExtensionStatus(status string) {
	f.hookStatus = status
}

// Apply handles footer bus messages.
func (f *FooterChrome) Apply(msg controller.FooterMsg) {
	switch msg.Kind {
	case controller.FooterSetActivity:
		f.activity.Apply(msg.Activity)
	case controller.FooterClearIfActivity:
		if f.activity.Current == msg.If {
			f.activity.Apply(controller.ActivityIdle)
		}
	case controller.FooterUpdateAvailable:
		latest := strings.TrimPrefix(msg.Latest, "v")
		f.updateHint = latest + " available · phi update"
	}
}

// ApplySessionEffects applies toast/status from session lifecycle extensions.
func (f *FooterChrome) ApplySessionEffects(msg controller.ExtSessionEffectsMsg) {
	if msg.StatusSet {
		f.hookStatus = msg.Status
	}
}

// syncStatusSlot writes the composer bottom-left label: activity while busy, else tokens.
func (f *FooterChrome) syncStatusSlot() {
	if f.composer == nil {
		return
	}
	var snap session.Snapshot
	if f.labelContext != nil {
		snap = f.labelContext()
	}
	if msg := f.activity.Label(snap); msg != "" {
		f.composer.SetBottomLeftLabel(f.activityStatusLabel(msg))
		return
	}
	if !f.lastUsage.Reported() {
		f.composer.ClearBottomLeftLabel()
		return
	}
	label := tokenStatusLabel(f.theme, f.lastUsage, f.contextWindow)
	if !label.Visible() {
		f.composer.ClearBottomLeftLabel()
		return
	}
	f.composer.SetBottomLeftLabel(label)
}

func (f *FooterChrome) activityStatusLabel(msg string) layout.BorderLabel {
	// Ambient chrome + typing-color sheen: one frame dialect, motion without a
	// competing brand hue (no ToolName cyan on the border).
	dim := ChromeLabelStyle(f.theme)
	if !f.activity.ShowSpinner() {
		return layout.BorderLabel{Text: msg, Style: dim}
	}
	on := f.theme.Foreground
	spans := make([]layout.BorderSpan, 0, len(msg)+1)
	f.spin.ForEachFlowCell(msg, func(ch string, lit bool) {
		st := dim
		if lit {
			st = on
		}
		spans = append(spans, layout.BorderSpan{Text: ch, Style: st})
	})
	return layout.BorderLabel{Spans: spans}
}

// Draw renders the bottom footer row (extension status, jobs, update hint).
// Activity/spinner live on the composer status slot, not here.
func (f *FooterChrome) Draw(ctx components.DrawContext, width int) components.Surface {
	footer := components.NewSurface(width, 1, nil)
	dim := f.theme.Muted
	var parts []string
	if hs := strings.TrimSpace(f.hookStatus); hs != "" {
		parts = append(parts, hs)
	}
	if f.liveJobs != nil {
		if n := f.liveJobs(); n > 0 {
			jobBit := fmt.Sprintf("%d job", n)
			if n != 1 {
				jobBit += "s"
			}
			parts = append(parts, jobBit)
		}
	}
	msg := strings.Join(parts, " · ")

	x := 1
	if msg != "" {
		footer.Print(x, 0, msg, dim, ctx.Method)
		x += xui.StringWidth(msg, ctx.Method)
	}

	hint := strings.TrimSpace(f.updateHint)
	if hint != "" {
		hw := xui.StringWidth(hint, ctx.Method)
		hx := width - hw - 1
		hx = max(hx, x+2)
		if hx+hw <= width {
			st := f.theme.TitleOrForeground()
			st.Bold = false
			footer.Print(hx, 0, hint, st, ctx.Method)
		}
	}
	return footer
}
