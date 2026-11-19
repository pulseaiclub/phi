// Package commands owns the TUI slash/palette command surface.
//
// Assembly:
//
//	builtins := commands.NewBuiltinRegistry(bus, ctrl, composer, ...)
//	builtins.Bind(openPicker, refreshPicker, openBranchPicker, refreshBranchPicker, openSkillPicker, cwd, streamActive)
//
// Domains (session, branch, settings, skills, diff) register themselves and
// hold Ctrl/Bus/Composer directly — no *Deps / *Params bags.
package commands
