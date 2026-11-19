package runtime

import (
	"reflect"

	"github.com/pulseaiclub/phi/internal/tui/controller"
)

// Dispatcher owns msg subscriptions and the drain-batch postludes. Handlers
// run in registration order; one msg activates every handler registered for
// its type. Registration must happen during Editor assembly, before the first
// Draw drains the bus.
type Dispatcher struct {
	msgs map[reflect.Type][]func(controller.Msg)

	batchStart  []func()
	batchEnd    []func()
	batchMarked bool
}

func newDispatcher() *Dispatcher {
	return &Dispatcher{msgs: make(map[reflect.Type][]func(controller.Msg))}
}

// add registers one handler for a concrete msg type.
func (d *Dispatcher) add(typ reflect.Type, fn func(controller.Msg)) {
	d.msgs[typ] = append(d.msgs[typ], fn)
}

// Dispatch applies m to the handlers registered for its type and reports
// whether any handler ran. UI goroutine only.
func (d *Dispatcher) Dispatch(m controller.Msg) bool {
	hs, ok := d.msgs[reflect.TypeOf(m)]
	if !ok {
		return false
	}
	for _, h := range hs {
		h(m)
	}
	return true
}

// OnBatchStart registers a hook that runs before every drained batch.
func (d *Dispatcher) OnBatchStart(fn func()) { d.batchStart = append(d.batchStart, fn) }

// OnBatchEnd registers a postlude that runs after a batch in which some
// handler called MarkBatchEvent.
func (d *Dispatcher) OnBatchEnd(fn func()) { d.batchEnd = append(d.batchEnd, fn) }

// MarkBatchEvent flags the current batch as carrying session work. Handlers
// call it on the UI goroutine while their handler runs.
func (d *Dispatcher) MarkBatchEvent() { d.batchMarked = true }

// DispatchBatch applies one drained batch: start hooks, per-msg dispatch,
// then end hooks when the batch was marked. The postlude order is the
// composition root's registration order — for the shell that is
// sync → activity → status slot → stick-to-bottom.
func (d *Dispatcher) DispatchBatch(batch []controller.Msg) {
	if len(batch) == 0 {
		return
	}
	for _, fn := range d.batchStart {
		fn()
	}
	d.batchMarked = false
	for _, m := range batch {
		d.Dispatch(m)
	}
	if d.batchMarked {
		for _, fn := range d.batchEnd {
			fn()
		}
	}
}
