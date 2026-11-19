package runtime

import "github.com/pulseaiclub/phi/internal/components"

// ThemeHub owns theme-switch fan-out. Callbacks run in registration order.
type ThemeHub struct {
	subs []func(components.Theme)
}

// On subscribes to theme switches.
func (th *ThemeHub) On(fn func(components.Theme)) {
	th.subs = append(th.subs, fn)
}

// Apply fans a theme switch out to every subscriber.
func (th *ThemeHub) Apply(t components.Theme) {
	for _, fn := range th.subs {
		fn(t)
	}
}
