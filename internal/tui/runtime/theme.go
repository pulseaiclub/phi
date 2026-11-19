package runtime

import "github.com/pulseaiclub/phi/internal/components"

// OnTheme subscribes to theme switches. Callbacks run in registration order.
func (rt *Runtime) OnTheme(fn func(components.Theme)) {
	rt.theme = append(rt.theme, fn)
}

// ApplyTheme fans a theme switch out to every subscriber.
func (rt *Runtime) ApplyTheme(th components.Theme) {
	for _, fn := range rt.theme {
		fn(th)
	}
}
