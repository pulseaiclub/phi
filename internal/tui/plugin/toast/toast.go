// Package toast owns the transient toast notification as a runtime plugin.
// Bus producers publish controller.ToastMsg; UI-goroutine callers (error
// dialog, theme switch, diff/code pickers) call Show synchronously so the
// toast is visible in the same frame, matching the pre-plugin shell.
package toast

import (
	"time"

	"github.com/pulseaiclub/phi/internal/components"
	uistoast "github.com/pulseaiclub/phi/internal/components/toast"
	"github.com/pulseaiclub/phi/internal/tui/controller"
	"github.com/pulseaiclub/phi/internal/tui/runtime"
)

// zToast layers the toast above every normal surface, below nothing.
const zToast = 40

// Plugin owns the toast state and renders it as the top overlay layer.
type Plugin struct {
	t uistoast.Toast
}

// Option customizes the toast plugin.
type Option func(*Plugin)

// WithClock overrides the wall clock. Tests and golden frames inject a fixed
// clock; production leaves it unset.
func WithClock(now func() time.Time) Option {
	return func(p *Plugin) { p.t.Now = now }
}

// New builds the toast plugin for the given theme.
func New(theme components.Theme, opts ...Option) *Plugin {
	p := &Plugin{t: uistoast.Toast{Theme: theme}}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Register wires the plugin into the runtime.
func (p *Plugin) Register(rt *runtime.Runtime) {
	runtime.Subscribe(rt, p.onToastMsg)
	rt.AddOverlay(zToast, p.draw)
	rt.OnTheme(p.SetTheme)
}

// Show displays message for d (default 2s) on the UI goroutine, in the same
// frame — callers must not hop through the bus for toast feedback.
func (p *Plugin) Show(message string, kind uistoast.ToastKind, d time.Duration) {
	p.t.Show(message, kind, d)
}

// Visible reports whether the toast should be drawn.
func (p *Plugin) Visible() bool { return p.t.Visible() }

// Current snapshots the toast state for tests.
func (p *Plugin) Current() uistoast.Toast { return p.t }

// SetTheme switches the toast chrome.
func (p *Plugin) SetTheme(th components.Theme) { p.t.Theme = th }

func (p *Plugin) onToastMsg(m controller.ToastMsg) {
	p.Show(m.Message, m.Kind, m.Duration)
}

func (p *Plugin) draw(ctx components.DrawContext) (components.Surface, bool) {
	if !p.t.Visible() {
		return components.Surface{}, false
	}
	return p.t.Draw(ctx), true
}
