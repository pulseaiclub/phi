// Package commands owns the TUI slash/palette command surface.
//
// Assembly:
//
//	builtins := commands.NewBuiltinRegistry(bus, ctrl, composer, ...)
//	builtins.Bind(submitter, commandCtx, openPicker, openBranchPicker, cwd, streamActive)
//
// Domains (session, branch, settings, skills, diff) register themselves
// hold Ctrl/Bus/Composer directly — no *Deps / *Params bags.
package commands
