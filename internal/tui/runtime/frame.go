package runtime

// FrameClock owns per-frame hooks. They run once per UI frame
// (Editor.Draw), before layout; the activity plugin ticks its spinner here.
// Hooks run in registration order.
type FrameClock struct {
	hooks []func()
}

// On registers a per-frame hook.
func (fc *FrameClock) On(fn func()) { fc.hooks = append(fc.hooks, fn) }

// Tick advances one UI frame.
func (fc *FrameClock) Tick() {
	for _, fn := range fc.hooks {
		fn()
	}
}
