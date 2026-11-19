package runtime

import "github.com/pulseaiclub/phi/internal/tui/controller"

// Batch hooks preserve the shell's drain-batch semantics: handlers mark
// session work with MarkBatchEvent, and the postludes run once per batch,
// in the order the composition root registered them. Registration order —
// not plugin registration — decides the Sync → activity → status → stick
// sequence.
type (
	BatchStart func()
	BatchEnd   func()
)

// OnBatchStart registers a hook that runs before every drained batch.
func (rt *Runtime) OnBatchStart(fn BatchStart) { rt.batchStart = append(rt.batchStart, fn) }

// OnBatchEnd registers a postlude that runs after a batch in which some
// handler called MarkBatchEvent.
func (rt *Runtime) OnBatchEnd(fn BatchEnd) { rt.batchEnd = append(rt.batchEnd, fn) }

// MarkBatchEvent flags the current batch as carrying session work. Handlers
// call it on the UI goroutine while their handler runs.
func (rt *Runtime) MarkBatchEvent() { rt.batchMarked = true }

// DispatchBatch applies one drained batch: start hooks, per-msg dispatch,
// then end hooks when the batch was marked.
func (rt *Runtime) DispatchBatch(batch []controller.Msg) {
	if len(batch) == 0 {
		return
	}
	for _, fn := range rt.batchStart {
		fn()
	}
	rt.batchMarked = false
	for _, m := range batch {
		rt.Dispatch(m)
	}
	if rt.batchMarked {
		for _, fn := range rt.batchEnd {
			fn()
		}
	}
}
