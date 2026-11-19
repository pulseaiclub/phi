package runtime

// FrameHook runs once per UI frame (Editor.Draw), before layout. The activity
// plugin ticks its spinner here; hooks run in registration order.
type FrameHook func()

// OnFrame registers a per-frame hook.
func (rt *Runtime) OnFrame(fn FrameHook) { rt.frameHooks = append(rt.frameHooks, fn) }

// Frame advances one UI frame.
func (rt *Runtime) Frame() {
	for _, fn := range rt.frameHooks {
		fn()
	}
}
