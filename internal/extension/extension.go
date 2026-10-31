package extension

import (
	"github.com/pulseaiclub/phi/internal/extension/core"
	"github.com/pulseaiclub/phi/internal/extension/ui"
)

// The facade below re-exports the subpackage surface so agent/tui/cmd can
// import just this package (see doc.go for the layer map). Runner, Fiber and
// FiberState live in core, mirroring cordis's single core package.

type (
	// Fiber is one extension's lifecycle state machine.
	Fiber = core.Fiber

	// FiberState is a fiber lifecycle state (pending/loading/active/failed/disposed/unloading).
	FiberState = core.FiberState

	// Runner aggregates all fibers, dispatches events and owns the per-extension
	// topology mutations (load / unload / reload).
	Runner = core.Runner

	// ModelRoute is one extension-registered model and the extension serving it.
	ModelRoute = core.ModelRoute

	// BusUI publishes extension UI effects onto host callbacks.
	BusUI = ui.BusUI
)

// Fiber states.
const (
	FiberPending  = core.FiberPending
	FiberLoading  = core.FiberLoading
	FiberActive   = core.FiberActive
	FiberFailed   = core.FiberFailed
	FiberDisposed = core.FiberDisposed
)
