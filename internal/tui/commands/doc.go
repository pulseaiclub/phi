// Package commands owns the TUI slash/palette command surface.
//
// Assembly:
//
//	r := commands.NewCommandRegistry()
//	submitter := submit.NewSubmitter(..., r, ...)
//	builtins := commands.NewBuiltinRegistry(r, ctx, bus, ctrl, composer, ..., ui, submitter)
//
// The registry has no domain deps, so it is built first; the submitter needs
// only that registry; the domains take the rest. Everything arrives through
// constructors — no *Deps / *Params bags, no late Bind pass.
//
// Domain callbacks to the shell aggregate in HostUI (implemented by the
// editor); composer callbacks in BuiltinComposer and the per-domain composer
// interfaces.
package commands
