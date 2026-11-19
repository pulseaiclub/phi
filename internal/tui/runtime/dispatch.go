package runtime

import (
	"reflect"

	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// Subscribe routes msgs of type T to h. Handlers run in registration order;
// one msg activates every handler registered for its type. Registration must
// happen during Editor assembly, before the first Draw drains the bus.
func Subscribe[T controller.Msg](rt *Runtime, h func(T)) {
	var zero T
	typ := reflect.TypeOf(zero)
	if typ == nil {
		panic("runtime: Subscribe needs a concrete msg type, not an interface")
	}
	rt.msgs[typ] = append(rt.msgs[typ], func(m controller.Msg) {
		h(any(m).(T))
	})
}

// Dispatch applies m to the handlers registered for its type and reports
// whether any handler ran. UI goroutine only.
func (rt *Runtime) Dispatch(m controller.Msg) bool {
	hs, ok := rt.msgs[reflect.TypeOf(m)]
	if !ok {
		return false
	}
	for _, h := range hs {
		h(m)
	}
	return true
}
